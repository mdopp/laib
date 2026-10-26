package templates

import (
	"strings"
	"testing"
)

// #567: Paperless' AI comes from the model role, as every other consumer's
// model does (${MODEL_URL}, ${MODEL_KEY}), and its embeddings go through the
// same proxy (owner, 2026-10-24): never llama-embed's own port, which a pod
// cannot reach and the owner does not open. The OCR settings that turned 11
// of 19 documents from letter salad into text are values, not hand settings.
func TestPaperlessAIFromTheModelRoleThroughTheProxy(t *testing.T) {
	unit, values := readT(t, "paperless/paperless.container"), readT(t, "paperless/values.example.yml")
	for _, want := range []string{
		"Environment=PAPERLESS_AI_ENABLED=${AI_ENABLED}\n",
		"Environment=PAPERLESS_AI_LLM_BACKEND=openai-like\n",
		"Environment=PAPERLESS_AI_LLM_ENDPOINT=${MODEL_URL}\n",
		"Environment=PAPERLESS_AI_LLM_API_KEY=${MODEL_KEY}\n",
		"Environment=PAPERLESS_AI_LLM_MODEL=${AI_MODEL}\n",
		"Environment=PAPERLESS_AI_LLM_EMBEDDING_BACKEND=${AI_EMBED_BACKEND}\n",
		"Environment=PAPERLESS_AI_LLM_EMBEDDING_MODEL=${AI_EMBED_MODEL}\n",
		"Environment=PAPERLESS_OCR_MODE=${OCR_MODE}\n",
		"Environment=PAPERLESS_OCR_ROTATE_PAGES=${OCR_ROTATE_PAGES}\n",
		"Environment=PAPERLESS_OCR_DESKEW=${OCR_DESKEW}\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("paperless.container lacks %q", strings.TrimSpace(want))
		}
	}
	if strings.Contains(unit, "EMBEDDING_ENDPOINT") || strings.Contains(unit, "11436") {
		t.Error("Paperless embeds past the proxy: llama-embed's own port is not for pods (#567)")
	}
	for _, want := range []string{"\nAI_ENABLED: \"true\"", "\nAI_EMBED_BACKEND: openai-like", "\nOCR_MODE: redo", "\nOCR_ROTATE_PAGES: \"true\""} {
		if !strings.Contains(values, want) {
			t.Errorf("values.example.yml lacks the default %q", strings.TrimSpace(want))
		}
	}
}
