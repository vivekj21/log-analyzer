package llm

import (
	"context"
	"log"
	"regexp"
	"strings"

	"k8s-log-analyzer/internal/protocol"
)

// categoryLineRe finds the closing line the prompt asks for: ROOT_CAUSE_CATEGORY: <CATEGORY>.
var categoryLineRe = regexp.MustCompile(`(?i)ROOT_CAUSE_CATEGORY[:\s*]*([A-Z_]+)`)

// ValidCategoryLine returns the category named on a ROOT_CAUSE_CATEGORY line, but only when it is
// one of AllCategories. Prose such as "OOMKilled" does not count: the report needs the exact name.
func ValidCategoryLine(text string) (string, bool) {
	for _, m := range categoryLineRe.FindAllStringSubmatch(text, -1) {
		c := strings.ToUpper(m[1])
		for _, k := range AllCategories {
			if c == k {
				return c, true
			}
		}
	}
	return "", false
}

const categoryRepairPrompt = "Your conclusion has no valid ROOT_CAUSE_CATEGORY line. Do not call tools. " +
	"Reply with exactly one line, ROOT_CAUSE_CATEGORY: <CATEGORY>, where CATEGORY is one of: %s. " +
	"Choose it from the evidence you already read. If that evidence does not show one cause, answer CANNOT_DETERMINE."

// CategoryGuard makes sure every final answer ends with a valid ROOT_CAUSE_CATEGORY line.
// If the model forgot it, the guard asks once for that single line in the same conversation.
// If that does not give a valid category either, the report says CANNOT_DETERMINE and says why:
// a missing category is never left blank and never guessed.
type CategoryGuard struct{ inner Model }

// NewCategoryGuard wraps a model that talks to a real service.
func NewCategoryGuard(inner Model) *CategoryGuard { return &CategoryGuard{inner: inner} }

// Forget passes cleanup through to the wrapped model.
func (g *CategoryGuard) Forget(invID string) {
	if f, ok := g.inner.(Forgetter); ok {
		f.Forget(invID)
	}
}

func (g *CategoryGuard) Analyze(ctx context.Context, invID, prompt string) (*AnalysisResult, error) {
	res, err := g.inner.Analyze(ctx, invID, prompt)
	return g.check(ctx, invID, res, err)
}

func (g *CategoryGuard) SendToolResults(ctx context.Context, invID string, r []protocol.ToolCallResult) (*AnalysisResult, error) {
	res, err := g.inner.SendToolResults(ctx, invID, r)
	return g.check(ctx, invID, res, err)
}

func (g *CategoryGuard) check(ctx context.Context, invID string, res *AnalysisResult, err error) (*AnalysisResult, error) {
	if err != nil || res == nil || res.HasToolCalls {
		return res, err
	}
	if _, ok := ValidCategoryLine(res.TextResponse); ok {
		return res, nil
	}
	log.Printf("[CategoryGuard] The answer has no valid ROOT_CAUSE_CATEGORY line; asking for it once")
	out := *res
	repair, rerr := g.inner.Analyze(ctx, invID, strings.Replace(categoryRepairPrompt, "%s", strings.Join(AllCategories, ", "), 1))
	if repair != nil {
		out.TotalTokens += repair.TotalTokens
	}
	if rerr == nil && repair != nil && !repair.HasToolCalls {
		if c, ok := ValidCategoryLine(repair.TextResponse); ok {
			out.TextResponse = strings.TrimRight(res.TextResponse, "\n ") + "\n\nROOT_CAUSE_CATEGORY: " + c + "\n"
			return &out, nil
		}
	}
	log.Printf("[CategoryGuard] Still no valid category; reporting %s", CategoryCannotDetermine)
	out.TextResponse = strings.TrimRight(res.TextResponse, "\n ") +
		"\n\nNote: the model did not name a valid root-cause category, so this is reported as " + CategoryCannotDetermine + ".\n\nROOT_CAUSE_CATEGORY: " + CategoryCannotDetermine + "\n"
	return &out, nil
}
