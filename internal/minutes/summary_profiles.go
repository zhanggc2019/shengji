package minutes

// SummaryProfile lists the model choices for a provider and keeps its API target server-side.
type SummaryProfile struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Models []string `json:"models,omitempty"`
	KeyEnv string   `json:"key_env,omitempty"`
	url    string
}

var summaryProfiles = []SummaryProfile{
	{ID: "ollama", Name: "Ollama"},
	{ID: "deepseek", Name: "DeepSeek", Models: []string{"deepseek-flash", "deepseek-v4-pro"},
		KeyEnv: "SHENGJI_DEEPSEEK_API_KEY", url: "https://api.deepseek.com/chat/completions"},
	{ID: "zhipu", Name: "智谱", Models: []string{"glm-5.3-flash", "glm-5.3", "glm-4.7-flash"},
		KeyEnv: "SHENGJI_ZHIPU_API_KEY", url: "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
	{ID: "siliconflow", Name: "硅基流动", Models: []string{"Qwen/Qwen3-32B", "deepseek-ai/DeepSeek-V3", "Pro/zai-org/GLM-5.1"},
		KeyEnv: "SHENGJI_SILICONFLOW_API_KEY", url: "https://api.siliconflow.cn/v1/chat/completions"},
	{ID: "openai", Name: "OpenAI 兼容（含 Groq）"},
	{ID: "claude", Name: "Claude"},
}

// summaryProfile finds the same provider used by the settings dialog and API client.
func summaryProfile(id string) (SummaryProfile, bool) {
	for _, profile := range summaryProfiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return SummaryProfile{}, false
}

func (profile SummaryProfile) endpoint(base string) string {
	if profile.url != "" {
		return profile.url
	}
	switch profile.ID {
	case "ollama":
		return endpoint(base, "/api/chat")
	case "claude":
		return endpoint(base, "/v1/messages")
	default:
		return endpoint(base, "/v1/chat/completions")
	}
}
