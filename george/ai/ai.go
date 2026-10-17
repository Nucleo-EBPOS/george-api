// Package ai é o ponto de extensão de IA (F4): resumo de sessão e busca
// natural de dispositivos. Usa a API Claude quando GEORGE_AI_KEY está definida;
// sem chave, os chamadores caem no fallback determinístico.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Client encapsula a chamada ao modelo.
type Client struct {
	model  string
	client anthropic.Client
	ok     bool
}

// New cria o cliente; Enabled() é false sem chave.
func New(apiKey, model string) *Client {
	if apiKey == "" {
		return &Client{}
	}
	return &Client{model: model, client: anthropic.NewClient(option.WithAPIKey(apiKey)), ok: true}
}

// Enabled diz se há chave configurada.
func (c *Client) Enabled() bool { return c != nil && c.ok }

func (c *Client) ask(ctx context.Context, system, user string, maxTokens int64) (string, error) {
	if !c.Enabled() {
		return "", errors.New("ia desabilitada")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: maxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(user))},
	})
	if err != nil {
		return "", err
	}
	if resp.StopReason == "refusal" {
		return "", errors.New("modelo recusou a solicitação")
	}
	var sb strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			sb.WriteString(t.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// Summarize gera a nota de atendimento a partir dos metadados/transcrição.
func (c *Client) Summarize(ctx context.Context, facts string) (string, error) {
	system := "Você escreve notas curtas de atendimento de suporte remoto em português do Brasil. " +
		"Use só os fatos fornecidos; não invente. Formato: 2 a 5 frases, sem título."
	return c.ask(ctx, system, facts, 1024)
}

// SearchFilter é o filtro estruturado que a busca natural produz.
type SearchFilter struct {
	Text        string `json:"text"`
	Online      *bool  `json:"online,omitempty"`
	HostEnabled *bool  `json:"host_enabled,omitempty"`
	Os          string `json:"os,omitempty"`
}

// ParseSearch converte a pergunta em filtro JSON.
func (c *Client) ParseSearch(ctx context.Context, q string) (SearchFilter, error) {
	system := `Converta a busca do usuário em um JSON com os campos {"text": string, "online": bool|null, ` +
		`"host_enabled": bool|null, "os": string}. "text" é o termo livre para procurar em nome/hostname/usuário. ` +
		`"os" é windows, macos, linux, android ou vazio. Responda SOMENTE com o JSON.`
	out, err := c.ask(ctx, system, q, 256)
	if err != nil {
		return SearchFilter{}, err
	}
	out = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(out), "```"), "```json")
	var f SearchFilter
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &f); err != nil {
		return SearchFilter{}, err
	}
	return f, nil
}

// HeuristicSearch é o fallback sem IA: entende online/offline, host:on/off, os:xxx e texto livre.
func HeuristicSearch(q string) SearchFilter {
	f := SearchFilter{}
	var free []string
	for _, tok := range strings.Fields(strings.ToLower(q)) {
		switch {
		case tok == "online" || tok == "ligado" || tok == "ligados" || tok == "ativos":
			t := true
			f.Online = &t
		case tok == "offline" || tok == "desligado" || tok == "desligados" || tok == "inativos":
			t := false
			f.Online = &t
		case tok == "host:on" || tok == "host:ligado":
			t := true
			f.HostEnabled = &t
		case tok == "host:off" || tok == "host:desligado":
			t := false
			f.HostEnabled = &t
		case strings.HasPrefix(tok, "os:"):
			f.Os = strings.TrimPrefix(tok, "os:")
		case tok == "windows" || tok == "linux" || tok == "macos" || tok == "mac" || tok == "android":
			if tok == "mac" {
				tok = "macos"
			}
			f.Os = tok
		default:
			free = append(free, tok)
		}
	}
	f.Text = strings.Join(free, " ")
	return f
}
