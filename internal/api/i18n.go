package api

import (
	"net/http"
	"strconv"
	"strings"
)

// Management messages default to Chinese for existing API clients. Selection
// is request-local; it never changes stored data, audit events, or MCP schemas.
func managementLocale(header string) string {
	best, locale := -1.0, "zh-CN"
	for _, item := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(item), ";")
		tag := strings.ToLower(strings.TrimSpace(fields[0]))
		candidate := ""
		switch {
		case tag == "en" || strings.HasPrefix(tag, "en-"):
			candidate = "en"
		case tag == "zh" || strings.HasPrefix(tag, "zh-") || tag == "*":
			candidate = "zh-CN"
		}
		if candidate == "" {
			continue
		}
		quality := 1.0
		for _, field := range fields[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(field), "=")
			if !ok || !strings.EqualFold(name, "q") {
				quality = 0
				break
			}
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil || !(parsed >= 0 && parsed <= 1) {
				quality = 0
				break
			}
			quality = parsed
		}
		if quality > 0 && quality > best {
			best, locale = quality, candidate
		}
	}
	return locale
}

func localizedMessage(w http.ResponseWriter, message string) string {
	if w.Header().Get("Content-Language") != "en" {
		return message
	}
	lines := strings.Split(message, "\n")
	for i, line := range lines {
		if translated, ok := englishMessages[line]; ok {
			lines[i] = translated
			continue
		}
		for source, translated := range englishPrefixes {
			if suffix, ok := strings.CutPrefix(line, source); ok {
				lines[i] = translated + suffix
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

var englishPrefixes = map[string]string{
	"执行登录身份检查失败: ": "Login identity check failed: ",
	"探测提权执行环境失败: ": "Privilege execution environment discovery failed: ",
	"提权执行失败: ":     "Privilege execution failed: ",
}

var englishMessages = map[string]string{
	"SSH 连接失败，请检查地址、凭据和主机密钥":               "SSH connection failed. Check the endpoint, credentials, and pinned host key",
	"主机公钥确认已过期，请重新预览":                      "Host key confirmation expired. Preview the key again",
	"分页参数不正确":                              "Invalid pagination parameters",
	"初始化凭据不正确或未启用 Web 初始化":                 "Invalid setup token or Web setup is disabled",
	"加密请求无效或已过期，请重新提交":                     "The encrypted request is invalid or expired. Submit it again",
	"加密请求用途不正确":                            "Invalid encrypted request purpose",
	"尝试次数过多，请稍后重试":                         "Too many attempts. Try again later",
	"当前密码不正确":                              "The current password is incorrect",
	"当前有配置等待应用，请刷新状态后重试应用，勿重复提交":           "Configuration application is pending. Refresh and retry application; do not submit the change again",
	"当前登录身份即为 root 用户":                     "The login identity already has UID 0",
	"待确认的主机公钥过多，请稍后重试":                     "Too many pending host key confirmations. Try again later",
	"提权身份检查失败：实际 UID 不是 0":                 "Privilege identity check failed: the actual UID is not 0",
	"操作失败，请刷新状态后检查服务":                      "Operation failed. Refresh the page and check the server",
	"操作已取消或超时，请刷新状态":                       "The operation was cancelled or timed out. Refresh the status",
	"新密码需为 12–72 字节":                       "The new password must contain 12–72 UTF-8 bytes",
	"无法探测远端 Shell，请检查凭据、主机公钥、网络及远端诊断命令权限":  "Shell discovery failed. Check credentials, host trust, connectivity, and remote diagnostic command permissions",
	"无法获取主机公钥，请检查地址、端口和网络":                 "Could not obtain the host key. Check the address, port, and network",
	"无法设置响应期限":                             "Could not set the response deadline",
	"无法设置请求期限":                             "Could not set the request deadline",
	"无法验证提权身份：远端未返回完整的 UID 和用户名":           "Could not verify privilege identity: the remote host did not return a complete UID and username",
	"版本标记不正确":                              "Invalid revision tag",
	"用户名或密码不正确":                            "Incorrect username or password",
	"用户名需为 1–64 字符，密码需为 12–72 字节":          "The username must contain 1–64 characters and the password 12–72 UTF-8 bytes",
	"登录繁忙，请稍后重试":                           "Login is busy. Try again later",
	"私钥与口令验证通过":                            "Private key and passphrase validated",
	"私钥口令不正确":                              "The private key passphrase is incorrect",
	"私钥未受密码保护，无需填写口令":                      "The private key is unencrypted. Leave the passphrase empty",
	"私钥未受密码保护，请清空私钥口令":                     "The private key is unencrypted. Clear the passphrase",
	"私钥格式不正确，无法解析":                         "Invalid or unsupported private key format",
	"管理员已初始化，请登录":                          "The administrator is already initialized. Sign in",
	"节点或上游跳板尚未启用":                          "The node or an upstream jump is disabled",
	"节点未配置提权方式（当前为 none）且无法验证免密 root 提权权限": "No privilege mode is configured (currently none), and passwordless root access could not be verified",
	"记录不存在或已删除":                            "The record does not exist or has been deleted",
	"该私钥受密码保护，请输入私钥口令":                     "This private key is encrypted. Enter its passphrase",
	"该记录仍被引用，请先解除关联":                       "The record is still referenced. Remove its associations first",
	"请先登录":        "Sign in first",
	"请刷新页面后再保存":   "Refresh the page before saving",
	"请发送 JSON 请求": "Send an application/json request",
	"请提供单个 SSH 主机公钥，不含选项或证书": "Provide one SSH host public key without options or a certificate",
	"请提供私钥内容": "Provide private key content",
	"请检查名称、到期时间和节点访问范围；绑定节点必须存在": "Check the name, expiration, and node scope; bound nodes must exist",
	"请求内容或字段不正确":                       "Invalid request content or fields",
	"请求必须只包含一个 JSON 对象":                "The request must contain exactly one JSON object",
	"请求来源不被允许":                         "The request origin is not allowed",
	"请求较多，请稍后重试":                       "Too many requests. Try again later",
	"远端具备免密 sudo (sudoer) 权限，建议在配置中开启": "Passwordless sudo is available. Consider enabling sudoer mode in the configuration",
	"连接测试繁忙，请稍后重试":                     "Connection tests are busy. Try again later",
	"配置已应用，但审计写入失败，请勿重复提交":             "Configuration applied, but audit logging failed. Do not submit the change again",
	"配置已被其他会话修改，请刷新后重试":                "Another session changed the configuration. Refresh and try again",
}
