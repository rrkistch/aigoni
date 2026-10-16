package server

import "encoding/json"

const displayFieldsJSON = `{"type":"object","label":"内容展示","properties":{"series":{"type":"string","label":"系列"},"seriesOrder":{"type":"number","label":"系列顺序","min":0,"max":100000},"comment":{"type":"boolean","label":"允许评论"},"licenseName":{"type":"string","label":"许可名称"},"licenseUrl":{"type":"string","label":"许可地址","format":"url"},"pinned":{"type":"boolean","label":"置顶"},"location":{"type":"string","label":"地点"},"status":{"type":"string","label":"项目状态"},"link":{"type":"array","label":"项目链接","maxItems":20,"items":{"type":"object","label":"链接","properties":{"label":{"type":"string","label":"名称"},"value":{"type":"string","label":"地址","format":"url"},"icon":{"type":"string","label":"图标"}}}}}}`

func displayField() adminField {
	var f adminField
	_ = json.Unmarshal([]byte(displayFieldsJSON), &f)
	return f
}
func publicDisplay(input map[string]any) map[string]any {
	out := map[string]any{}
	spec := displayField()
	// Explicit allowlist, including for Markdown created outside the admin API.
	for k, v := range input {
		if field, ok := spec.Properties[k]; ok {
			b, e := json.Marshal(v)
			var normalized any
			if e == nil && json.Unmarshal(b, &normalized) == nil && validateAdminValue(field, normalized, k) == nil {
				out[k] = normalized
			}
		}
	}
	return out
}
