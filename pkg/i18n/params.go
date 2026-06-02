package i18n

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"text/template"
)

// Params 是翻译模板变量，只用于 message interpolation，不进入响应 details。
// 与 Details 使用不同命名类型，避免调用方把内部字段误放进客户端响应体。
type Params map[string]any

var (
	// ErrSensitiveParamKey 表示 Params 中出现了不允许进入翻译模板的数据 key。
	ErrSensitiveParamKey = errors.New("i18n: sensitive param key")

	sensitiveParamKeyPattern = regexp.MustCompile(`(?i)(uid|token|sql|secret|internal_id|password|raw_err)`)
)

// SensitiveParamKeyError 记录被拦截的 Params key。
type SensitiveParamKeyError struct {
	Key string
}

func (e SensitiveParamKeyError) Error() string {
	return fmt.Sprintf("i18n: sensitive param key %q is not allowed", e.Key)
}

func (e SensitiveParamKeyError) Unwrap() error {
	return ErrSensitiveParamKey
}

// IsSensitiveParamKey 返回 key 是否命中 D15/CI 约定的敏感字段正则。
func IsSensitiveParamKey(key string) bool {
	return sensitiveParamKeyPattern.MatchString(key)
}

// TemplateData 校验 Params 并返回深一层 map 副本，供 go-i18n TemplateData 使用。
// nil Params 返回 nil，便于无模板变量路径零分配传递。
func (p Params) TemplateData() (map[string]any, error) {
	if p == nil {
		return nil, nil
	}
	out := make(map[string]any, len(p))
	for k, v := range p {
		if IsSensitiveParamKey(k) {
			return nil, SensitiveParamKeyError{Key: k}
		}
		out[k] = v
	}
	return out, nil
}

// Render 用 Params 渲染一个 Go template 字符串。缺失 key 返回错误，由调用方
// 决定是否回退到未插值文案。
func (p Params) Render(tmpl string) (string, error) {
	data, err := p.TemplateData()
	if err != nil {
		return "", err
	}
	t, err := template.New("i18n_message").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
