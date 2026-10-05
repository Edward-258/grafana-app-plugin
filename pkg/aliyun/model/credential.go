package model

// Credential 是一对 AK/SK 及其在插件内的标识。ID 是插槽主键：已保存对用
// 前端生成的随机 slot uuid（jsonData.akList），legacy 单对兜底用固定值
// LegacyCredentialID。jsonData / secureJsonData 的键名都由它构造，任何位置
// 都不放 AK 明文（红线4：jsonData 只允许 uuid、label 这类非敏感值）。
type Credential struct {
	ID              string
	Label           string
	AccessKeyID     string
	AccessKeySecret string
}

// Config 还原单凭证配置，供阿里云 client 调用。
func (c Credential) Config() Config {
	return Config{AccessKeyID: c.AccessKeyID, AccessKeySecret: c.AccessKeySecret}
}

// LegacyCredentialID 是升级前的单对凭证（secureJsonData 的
// accessKeyId/accessKeySecret 键）在插槽体系里的固定主键。
const LegacyCredentialID = "legacy"
