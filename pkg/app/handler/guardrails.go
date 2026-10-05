package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"local-ecs-app/pkg/app/service"
)

// 请求守卫：限额与校验集中在此，handler 只调用，不各自为政。
// 限额取十倍于真实负载的余量，调整时改这里并跑测试。
const (
	// maxBodyBytes 单请求体上限。enrich 的真实负载是 Prometheus 的 up/node_uname_info
	// 序列（几百条 × ~100B），1MB 是十倍以上余量；gRPC 转发本身允许 16MB，
	// 这里在解码器之前把放大源掐掉。
	maxBodyBytes = 1 << 20

	// DefaultMaxIdentities 单次 enrich 的标识条数上限，是 CPU 放大的直接开关：
	// 每条标识最多触发 3 串 × 4 步的资产列表线性扫描，条数失控 = 数亿次比较。
	DefaultMaxIdentities = 2000

	// maxAKPairs 凭证插槽上限。AK/SK 是低频配置数据，50 远超真实需求；兜底
	// 防止 jsonData/secureJsonData 无界膨胀（设置保存不经插件 handler，写入侧
	// 只能靠 UI 约束，这里守读取侧）。
	maxAKPairs = 50
)

var (
	ErrTooManyIdentities = errors.New("prometheus 标识数量超过上限")
	ErrTooManyAKPairs    = errors.New("AK/SK 对数量超过上限")
)

// ValidateAKPairCount 挡凭证对数量放大：每对都意味着一轮全地域枚举 + BSS 调用。
func ValidateAKPairCount(n int) error {
	if n > maxAKPairs {
		return fmt.Errorf("%w: %d 对 > %d 对", ErrTooManyAKPairs, n, maxAKPairs)
	}
	return nil
}

// decodeBody 统一请求体闸：先套字节上限再解码，超限 413、坏格式 400。
// 返回 false 时响应已写完，调用方直接 return。
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "请求体过大，上限 1MB"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无效的请求体"})
		}
		return false
	}
	return true
}

// ValidateIdentities 挡数量放大：body 不超 1MB 仍可能塞进上万条小对象，
// 条数必须在语义层单独设限。
func ValidateIdentities(ids []service.Identity) error {
	if len(ids) > DefaultMaxIdentities {
		return fmt.Errorf("%w: %d 条 > %d 条", ErrTooManyIdentities, len(ids), DefaultMaxIdentities)
	}
	return nil
}
