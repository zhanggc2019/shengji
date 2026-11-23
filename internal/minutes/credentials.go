package minutes

import (
	"database/sql"
	"errors"
	"strings"
)

type credentialChange struct {
	scope string
	key   string
}

type credentialStatus struct {
	CanManage bool `json:"can_manage"`
	ASRSaved  bool `json:"asr_saved"`
	LLMSaved  bool `json:"llm_saved"`
}

func (c Settings) asrEndpoint() string {
	return endpoint(c.ASRURL, "/v1/audio/transcriptions")
}

// Credential scopes bind a key to its provider and actual request URL.
func (c Settings) asrCredentialScope() string {
	switch c.ASRProvider {
	case "openai", "groq":
		return "asr:" + c.ASRProvider + ":" + c.asrEndpoint()
	default:
		return ""
	}
}

func (c Settings) summaryCredentialScope() string {
	profile, ok := summaryProfile(c.Provider)
	if !ok {
		return ""
	}
	return "summary:" + c.Provider + ":" + profile.endpoint(c.LLMURL)
}

func (status *credentialStatus) read(db *sql.DB, c Settings) error {
	return db.QueryRow(`SELECT EXISTS(SELECT 1 FROM model_credentials WHERE scope=?),
		EXISTS(SELECT 1 FROM model_credentials WHERE scope=?)`,
		c.asrCredentialScope(), c.summaryCredentialScope()).Scan(&status.ASRSaved, &status.LLMSaved)
}

func (a *App) credential(scope string) (string, error) {
	if scope == "" {
		return "", nil
	}
	var blob []byte
	err := a.db.QueryRow("SELECT secret FROM model_credentials WHERE scope=?", scope).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("读取已保存的 API Key 失败")
	}
	secret, err := unprotectCredential(a.dir, scope, blob)
	if err != nil {
		return "", errors.New("无法读取已保存的 API Key，请在「模型设置」重新填写")
	}
	return secret, nil
}

func (a *App) saveCredential(tx *sql.Tx, change credentialChange) error {
	if change.key == "" {
		_, err := tx.Exec("DELETE FROM model_credentials WHERE scope=?", change.scope)
		return err
	}
	blob, err := protectCredential(a.dir, change.scope, change.key)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO model_credentials(scope,secret) VALUES(?,?)
		ON CONFLICT(scope) DO UPDATE SET secret=excluded.secret`, change.scope, blob)
	return err
}

func validAPIKey(key string) bool {
	return len(key) <= 4096 && strings.IndexFunc(key, func(r rune) bool { return r < '!' || r > '~' }) == -1
}
