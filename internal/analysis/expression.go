package analysis

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Expression struct {
	root node
}

func CompileExpression(source string) (*Expression, error) {
	parser := expressionParser{tokens: tokenize(source)}
	if parser.tokens.err != nil {
		return nil, parser.tokens.err
	}
	root, err := parser.parseExpression()
	if err != nil {
		return nil, err
	}
	if parser.peek().kind != tokenEOF {
		return nil, fmt.Errorf("unexpected token %q", parser.peek().text)
	}
	return &Expression{root: root}, nil
}

func (expression *Expression) Evaluate(fields map[string]any) (any, error) {
	if expression == nil || expression.root == nil {
		return nil, fmt.Errorf("expression is empty")
	}
	return expression.root.evaluate(fields)
}

type node interface {
	evaluate(fields map[string]any) (any, error)
}

type literalNode struct{ value any }

func (literal literalNode) evaluate(map[string]any) (any, error) { return literal.value, nil }

type fieldNode struct{ name string }

func (field fieldNode) evaluate(fields map[string]any) (any, error) {
	value, ok := fields[field.name]
	if !ok || value == nil {
		return nil, fmt.Errorf("field %q is null or missing", field.name)
	}
	return value, nil
}

type unaryNode struct {
	operator string
	child    node
}

func (unary unaryNode) evaluate(fields map[string]any) (any, error) {
	value, err := unary.child.evaluate(fields)
	if err != nil {
		return nil, err
	}
	number, err := toFloat(value)
	if err != nil {
		return nil, err
	}
	if unary.operator == "-" {
		return -number, nil
	}
	return number, nil
}

type binaryNode struct {
	operator    string
	left, right node
}

func (binary binaryNode) evaluate(fields map[string]any) (any, error) {
	left, err := binary.left.evaluate(fields)
	if err != nil {
		return nil, err
	}
	right, err := binary.right.evaluate(fields)
	if err != nil {
		return nil, err
	}
	leftNumber, err := toFloat(left)
	if err != nil {
		return nil, err
	}
	rightNumber, err := toFloat(right)
	if err != nil {
		return nil, err
	}
	switch binary.operator {
	case "+":
		return leftNumber + rightNumber, nil
	case "-":
		return leftNumber - rightNumber, nil
	case "*":
		return leftNumber * rightNumber, nil
	case "/":
		if rightNumber == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return leftNumber / rightNumber, nil
	default:
		return nil, fmt.Errorf("unsupported operator %q", binary.operator)
	}
}

type functionNode struct {
	name string
	args []node
}

func (function functionNode) evaluate(fields map[string]any) (any, error) {
	values := make([]any, len(function.args))
	for index, argument := range function.args {
		value, err := argument.evaluate(fields)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	switch function.name {
	case "lowercase":
		if len(values) != 1 {
			return nil, fmt.Errorf("lowercase expects one argument")
		}
		return strings.ToLower(fmt.Sprint(values[0])), nil
	case "uppercase":
		if len(values) != 1 {
			return nil, fmt.Errorf("uppercase expects one argument")
		}
		return strings.ToUpper(fmt.Sprint(values[0])), nil
	case "to_integer":
		if len(values) != 1 {
			return nil, fmt.Errorf("to_integer expects one argument")
		}
		value, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(values[0])), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot convert %q to integer", values[0])
		}
		return value, nil
	case "to_float":
		if len(values) != 1 {
			return nil, fmt.Errorf("to_float expects one argument")
		}
		return toFloat(values[0])
	case "duration_between":
		if len(values) != 2 {
			return nil, fmt.Errorf("duration_between expects two arguments")
		}
		start, err := toTime(values[0])
		if err != nil {
			return nil, err
		}
		end, err := toTime(values[1])
		if err != nil {
			return nil, err
		}
		return end.Sub(start).Seconds(), nil
	default:
		return nil, fmt.Errorf("function %q is not allowed", function.name)
	}
}

func toFloat(value any) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case int32:
		return float64(typed), nil
	case jsonNumber:
		return strconv.ParseFloat(string(typed), 64)
	default:
		number, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not numeric", value)
		}
		return number, nil
	}
}

type jsonNumber string

func toTime(value any) (time.Time, error) {
	if typed, ok := value.(time.Time); ok {
		return typed, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, fmt.Sprint(value))
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not an RFC3339 timestamp", value)
	}
	return parsed, nil
}

type tokenKind int

const (
	tokenInvalid tokenKind = iota
	tokenEOF
	tokenNumber
	tokenString
	tokenIdentifier
	tokenOperator
	tokenLeftParen
	tokenRightParen
	tokenComma
)

type token struct {
	kind tokenKind
	text string
}

type tokenStream struct {
	items []token
	err   error
}

func tokenize(source string) tokenStream {
	items := make([]token, 0)
	for index := 0; index < len(source); {
		current := rune(source[index])
		if unicode.IsSpace(current) {
			index++
			continue
		}
		switch source[index] {
		case '+', '-', '*', '/':
			items = append(items, token{kind: tokenOperator, text: source[index : index+1]})
			index++
		case '(':
			items = append(items, token{kind: tokenLeftParen, text: "("})
			index++
		case ')':
			items = append(items, token{kind: tokenRightParen, text: ")"})
			index++
		case ',':
			items = append(items, token{kind: tokenComma, text: ","})
			index++
		case '\'', '"':
			quote := source[index]
			start := index + 1
			index++
			for index < len(source) && source[index] != quote {
				index++
			}
			if index >= len(source) {
				return tokenStream{err: fmt.Errorf("unterminated string literal")}
			}
			items = append(items, token{kind: tokenString, text: source[start:index]})
			index++
		default:
			if unicode.IsDigit(current) || source[index] == '.' {
				start := index
				index++
				for index < len(source) && (unicode.IsDigit(rune(source[index])) || source[index] == '.') {
					index++
				}
				if _, err := strconv.ParseFloat(source[start:index], 64); err != nil {
					return tokenStream{err: fmt.Errorf("invalid number %q", source[start:index])}
				}
				items = append(items, token{kind: tokenNumber, text: source[start:index]})
				continue
			}
			if unicode.IsLetter(current) || source[index] == '_' {
				start := index
				index++
				for index < len(source) {
					value := rune(source[index])
					if !unicode.IsLetter(value) && !unicode.IsDigit(value) && source[index] != '_' {
						break
					}
					index++
				}
				items = append(items, token{kind: tokenIdentifier, text: source[start:index]})
				continue
			}
			return tokenStream{err: fmt.Errorf("unexpected character %q", source[index])}
		}
	}
	items = append(items, token{kind: tokenEOF})
	return tokenStream{items: items}
}

type expressionParser struct {
	tokens tokenStream
	index  int
}

func (parser *expressionParser) peek() token {
	if parser.index >= len(parser.tokens.items) {
		return token{kind: tokenEOF}
	}
	return parser.tokens.items[parser.index]
}

func (parser *expressionParser) take() token {
	current := parser.peek()
	if parser.index < len(parser.tokens.items) {
		parser.index++
	}
	return current
}

func (parser *expressionParser) parseExpression() (node, error) {
	left, err := parser.parseTerm()
	if err != nil {
		return nil, err
	}
	for parser.peek().kind == tokenOperator && (parser.peek().text == "+" || parser.peek().text == "-") {
		operator := parser.take().text
		right, err := parser.parseTerm()
		if err != nil {
			return nil, err
		}
		left = binaryNode{operator: operator, left: left, right: right}
	}
	return left, nil
}

func (parser *expressionParser) parseTerm() (node, error) {
	left, err := parser.parseFactor()
	if err != nil {
		return nil, err
	}
	for parser.peek().kind == tokenOperator && (parser.peek().text == "*" || parser.peek().text == "/") {
		operator := parser.take().text
		right, err := parser.parseFactor()
		if err != nil {
			return nil, err
		}
		left = binaryNode{operator: operator, left: left, right: right}
	}
	return left, nil
}

func (parser *expressionParser) parseFactor() (node, error) {
	current := parser.take()
	switch current.kind {
	case tokenNumber:
		value, _ := strconv.ParseFloat(current.text, 64)
		return literalNode{value: value}, nil
	case tokenString:
		return literalNode{value: current.text}, nil
	case tokenOperator:
		if current.text != "+" && current.text != "-" {
			return nil, fmt.Errorf("unexpected operator %q", current.text)
		}
		child, err := parser.parseFactor()
		return unaryNode{operator: current.text, child: child}, err
	case tokenIdentifier:
		if parser.peek().kind != tokenLeftParen {
			return fieldNode{name: current.text}, nil
		}
		parser.take()
		arguments := make([]node, 0)
		if parser.peek().kind != tokenRightParen {
			for {
				argument, err := parser.parseExpression()
				if err != nil {
					return nil, err
				}
				arguments = append(arguments, argument)
				if parser.peek().kind != tokenComma {
					break
				}
				parser.take()
			}
		}
		if parser.take().kind != tokenRightParen {
			return nil, fmt.Errorf("function %q has an unclosed argument list", current.text)
		}
		if current.text != "lowercase" && current.text != "uppercase" &&
			current.text != "to_integer" && current.text != "to_float" &&
			current.text != "duration_between" {
			return nil, fmt.Errorf("function %q is not allowed", current.text)
		}
		return functionNode{name: current.text, args: arguments}, nil
	case tokenLeftParen:
		child, err := parser.parseExpression()
		if err != nil {
			return nil, err
		}
		if parser.take().kind != tokenRightParen {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		return child, nil
	default:
		return nil, fmt.Errorf("unexpected token %q", current.text)
	}
}
