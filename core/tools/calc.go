package tools

import (
	"fmt"

	"github.com/Knetic/govaluate"
	"google.golang.org/adk/tool"
)

type CalcIn struct {
	Expression string `json:"expression"`
}
type CalcOut struct {
	Result string `json:"result"`
}

func NewCalcTools() []tool.Tool {
	return []tool.Tool{
		mustTool("calculate",
			"Evaluate a mathematical expression and return the result. "+
				"Supports arithmetic (+, -, *, /), exponentiation (**), modulo (%), "+
				"bitwise ops, comparisons, and standard functions (abs, ceil, floor, round, sqrt, log, pow, sin, cos, tan). "+
				"Arguments: `expression` (string, required) — the expression to evaluate, e.g. \"sqrt(2) + 3 * 4\".",
			func(_ tool.Context, in CalcIn) (CalcOut, error) {
				expr, err := govaluate.NewEvaluableExpression(in.Expression)
				if err != nil {
					return CalcOut{Result: fmt.Sprintf("parse error: %v", err)}, nil
				}
				result, err := expr.Evaluate(nil)
				if err != nil {
					return CalcOut{Result: fmt.Sprintf("eval error: %v", err)}, nil
				}
				return CalcOut{Result: fmt.Sprintf("%v", result)}, nil
			}),
	}
}
