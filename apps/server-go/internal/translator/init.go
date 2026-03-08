package translator

import (
	// Shared format bridges still required by the retained providers.
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/openai/claude"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/openai/openai/responses"

	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/gemini"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/openai/chat-completions"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/openai/responses"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/auggie/openai/chat-completions"
)
