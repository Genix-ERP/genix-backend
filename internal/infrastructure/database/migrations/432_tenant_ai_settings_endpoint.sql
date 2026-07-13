-- Per-tenant AI endpoint override. Lets a tenant point their own provider/key
-- at a specific base URL (e.g. an OpenAI-compatible endpoint like Google
-- Gemini's, or Azure OpenAI). Blank = use the provider's default endpoint.
ALTER TABLE tenant_ai_settings ADD COLUMN IF NOT EXISTS endpoint TEXT NOT NULL DEFAULT '';
