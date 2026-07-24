package analysis

import "testing"

func TestSafeDerivedExpressions(t *testing.T) {
	expression, err := CompileExpression("damage / duration_seconds")
	if err != nil {
		t.Fatal(err)
	}
	value, err := expression.Evaluate(map[string]any{
		"damage": 84, "duration_seconds": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.(float64) != 42 {
		t.Fatalf("derived value = %v, want 42", value)
	}

	expression, err = CompileExpression("uppercase(player)")
	if err != nil {
		t.Fatal(err)
	}
	value, err = expression.Evaluate(map[string]any{"player": "Andrew"})
	if err != nil || value != "ANDREW" {
		t.Fatalf("uppercase result = %v, err = %v", value, err)
	}
}

func TestArbitraryFunctionsAndDivisionByZeroAreRejected(t *testing.T) {
	if _, err := CompileExpression("eval(payload)"); err == nil {
		t.Fatal("arbitrary function was accepted")
	}
	expression, err := CompileExpression("damage / duration")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expression.Evaluate(map[string]any{"damage": 1, "duration": 0}); err == nil {
		t.Fatal("division by zero was accepted")
	}
}

func TestMalformedExpressionsReturnErrorsWithoutPanicking(t *testing.T) {
	for _, source := range []string{"", "(", "lowercase(", "1 +", "duration_between(timestamp,"} {
		if _, err := CompileExpression(source); err == nil {
			t.Fatalf("CompileExpression(%q) succeeded", source)
		}
	}
}
