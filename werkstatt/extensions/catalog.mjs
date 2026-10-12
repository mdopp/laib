// The lease's catalogue (/v1/models from laib-lease: id, allowed_in_mode, no
// `pi` object) as pi models. Pure, so a test can run it without pi-ai; the
// extension imports it. The provider id is defined here, once (#44).
export const PROVIDER_ID = "laib-llama";

function positive(value, fallback) {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? Math.floor(value) : fallback;
}

export function toModel(entry, baseUrl) {
  const pi = entry && typeof entry.pi === "object" && entry.pi !== null ? entry.pi : {};
  const input = Array.isArray(pi.input) && pi.input.length > 0 ? pi.input : ["text"];
  return {
    id: entry.id,
    name: typeof pi.name === "string" && pi.name ? pi.name : entry.id,
    api: "openai-completions",
    provider: PROVIDER_ID,
    baseUrl,
    reasoning: pi.reasoning === true,
    input,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: positive(pi.contextWindow, 32768),
    maxTokens: positive(pi.maxTokens, 16384),
    compat: typeof pi.compat === "object" && pi.compat !== null ? pi.compat : {},
  };
}

// fromCatalog: every entry with an id is a model — the lease marks what the
// standing mode allows (allowed_in_mode), it does not describe pi options.
export function fromCatalog(payload, baseUrl = "") {
  const data = payload && Array.isArray(payload.data) ? payload.data : [];
  return data.filter((entry) => entry && typeof entry.id === "string").map((entry) => toModel(entry, baseUrl));
}
