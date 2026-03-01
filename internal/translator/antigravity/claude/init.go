package claude

import (
	. "github.com/router-for-me/CLIProxyAPI/v6/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	optimizedclaude "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude/optimized"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/translator/translator"
)

func init() {
	translator.Register(
		Claude,
		Antigravity,
		optimizedclaude.ConvertClaudeRequestToAntigravity,
		interfaces.TranslateResponse{
			Stream:     optimizedclaude.ConvertAntigravityResponseToClaude,
			NonStream:  optimizedclaude.ConvertAntigravityResponseToClaudeNonStream,
			TokenCount: ClaudeTokenCount,
		},
	)
}
