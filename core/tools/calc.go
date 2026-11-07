package tools

import (
	"fmt"

	"github.com/Knetic/govaluate"
	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
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
			"Evaluate a math expression, e.g. \"sqrt(2) + 3 * 4\": + - * / ** %, comparisons, bitwise ops, "+
				"abs ceil floor round sqrt log pow sin cos tan.",
			func(_ adk.ToolContext, in CalcIn) (CalcOut, error) {
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
