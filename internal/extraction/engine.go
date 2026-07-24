package extraction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"loglens/internal/domain"
	"loglens/internal/parser"
)

type Engine struct {
	version       domain.ParserProfileVersion
	fields        map[string]domain.FieldDefinition
	rules         []compiledRule
	allowMultiple bool
}

type compiledRule struct {
	rule       domain.ExtractionRule
	regex      *regexp.Regexp
	fieldMap   map[string]string
	required   map[string]string
	eventField string
	eventValue string
}

type rawMatch struct {
	rule   compiledRule
	values map[string]any
}

func Compile(version domain.ParserProfileVersion) (*Engine, error) {
	engine := &Engine{
		version: version,
		fields:  make(map[string]domain.FieldDefinition, len(version.Fields)),
	}
	if value, ok := version.Configuration["allowMultiple"].(bool); ok {
		engine.allowMultiple = value
	}
	for _, field := range version.Fields {
		if !validFieldName(field.Name) {
			return nil, fmt.Errorf("invalid field name %q", field.Name)
		}
		if !validFieldType(field.FieldType) {
			return nil, fmt.Errorf("unsupported field type %q", field.FieldType)
		}
		if _, exists := engine.fields[field.Name]; exists {
			return nil, fmt.Errorf("duplicate field %q", field.Name)
		}
		engine.fields[field.Name] = field
	}
	rules := append([]domain.ExtractionRule(nil), version.Rules...)
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority == rules[j].Priority {
			return rules[i].ID < rules[j].ID
		}
		return rules[i].Priority < rules[j].Priority
	})
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		compiled, err := compileRule(rule)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", rule.Name, err)
		}
		engine.rules = append(engine.rules, compiled)
	}
	return engine, nil
}

func (engine *Engine) Extract(raw []byte, envelope parser.Result) domain.ExtractionResult {
	result := domain.ExtractionResult{Fields: map[string]domain.FieldValue{}}
	matches := make([]rawMatch, 0, 2)
	for _, rule := range engine.rules {
		values, matched := rule.match(raw, envelope)
		if matched {
			matches = append(matches, rawMatch{rule: rule, values: values})
			if engine.allowMultiple {
				continue
			}
		}
	}
	if len(matches) == 0 {
		return result
	}
	first := matches[0]
	result.EventType = first.rule.rule.EventType
	result.MatchedRuleID = &first.rule.rule.ID
	if len(matches) > 1 && !engine.allowMultiple {
		result.Warnings = append(result.Warnings, domain.ParseWarning{
			Code: "ambiguous_rule",
			Message: fmt.Sprintf("rules %q and %q both matched; priority selected %q",
				matches[0].rule.rule.Name, matches[1].rule.rule.Name, matches[0].rule.rule.Name),
		})
		matches = matches[:1]
	}
	for _, match := range matches {
		for name, rawValue := range match.values {
			field, exists := engine.fields[name]
			if !exists {
				text := stringify(rawValue)
				result.Fields[name] = domain.FieldValue{Type: domain.FieldString, StringValue: &text}
				result.Warnings = append(result.Warnings, domain.ParseWarning{
					Code: "undefined_field", Field: name,
					Message: "The matcher extracted a field that has no field definition",
				})
				continue
			}
			value, warning := convert(field, rawValue)
			if warning != nil {
				result.Warnings = append(result.Warnings, *warning)
				continue
			}
			result.Fields[name] = value
		}
	}
	for name, field := range engine.fields {
		if !field.Nullable {
			if _, exists := result.Fields[name]; !exists {
				result.Warnings = append(result.Warnings, domain.ParseWarning{
					Code: "required_field_missing", Field: name,
					Message: "A required field was not extracted",
				})
			}
		}
	}
	return result
}

func (engine *Engine) Version() domain.ParserProfileVersion {
	return engine.version
}

func (engine *Engine) Field(name string) (domain.FieldDefinition, bool) {
	field, ok := engine.fields[name]
	return field, ok
}

func compileRule(rule domain.ExtractionRule) (compiledRule, error) {
	compiled := compiledRule{
		rule:     rule,
		fieldMap: stringMap(rule.MatcherConfig["fieldMap"]),
		required: stringMap(rule.MatcherConfig["required"]),
	}
	compiled.eventField, _ = rule.MatcherConfig["eventField"].(string)
	compiled.eventValue, _ = rule.MatcherConfig["eventValue"].(string)
	switch rule.MatcherType {
	case "regex":
		pattern, _ := rule.MatcherConfig["pattern"].(string)
		if strings.TrimSpace(pattern) == "" {
			return compiled, fmt.Errorf("pattern is required")
		}
		expression, err := regexp.Compile(normalizeNamedCaptures(pattern))
		if err != nil {
			return compiled, fmt.Errorf("invalid regex: %w", err)
		}
		compiled.regex = expression
	case "literal_pattern":
		pattern, _ := rule.MatcherConfig["pattern"].(string)
		expression, err := compileLiteralPattern(pattern)
		if err != nil {
			return compiled, err
		}
		compiled.regex = expression
	case "key_value", "json":
		// These matchers are decoded without a regular expression.
	default:
		return compiled, fmt.Errorf("unsupported matcher type %q", rule.MatcherType)
	}
	return compiled, nil
}

func (rule compiledRule) match(raw []byte, envelope parser.Result) (map[string]any, bool) {
	var values map[string]any
	switch rule.rule.MatcherType {
	case "regex", "literal_pattern":
		match := rule.regex.FindSubmatch(bytes.TrimRight(raw, "\r\n"))
		if match == nil {
			return nil, false
		}
		values = map[string]any{}
		for index, name := range rule.regex.SubexpNames() {
			if index == 0 || name == "" || index >= len(match) || match[index] == nil {
				continue
			}
			target := name
			if mapped := rule.fieldMap[name]; mapped != "" {
				target = mapped
			}
			values[target] = string(match[index])
		}
	case "key_value":
		values = parseKeyValues(string(raw))
	case "json":
		if err := json.Unmarshal(bytes.TrimSpace(raw), &values); err != nil || values == nil {
			return nil, false
		}
		values = flattenMapped(values, rule.fieldMap)
	}
	if rule.eventField != "" && stringify(values[rule.eventField]) != rule.eventValue {
		return nil, false
	}
	for field, expected := range rule.required {
		if stringify(values[field]) != expected {
			return nil, false
		}
	}
	if len(rule.fieldMap) > 0 && rule.rule.MatcherType == "key_value" {
		values = flattenMapped(values, rule.fieldMap)
	}
	if len(values) == 0 && envelope.Message == "" {
		return nil, false
	}
	return values, true
}

func convert(field domain.FieldDefinition, raw any) (domain.FieldValue, *domain.ParseWarning) {
	text := strings.TrimSpace(stringify(raw))
	warning := func(code string) (domain.FieldValue, *domain.ParseWarning) {
		return domain.FieldValue{}, &domain.ParseWarning{
			Code: code, Field: field.Name,
			Message: fmt.Sprintf("value %q is not a valid %s", text, field.FieldType),
		}
	}
	switch field.FieldType {
	case domain.FieldString:
		return domain.FieldValue{Type: field.FieldType, StringValue: &text}, nil
	case domain.FieldInteger:
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return warning("invalid_integer")
		}
		return domain.FieldValue{Type: field.FieldType, IntegerValue: &value}, nil
	case domain.FieldFloat:
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return warning("invalid_float")
		}
		return domain.FieldValue{Type: field.FieldType, FloatValue: &value}, nil
	case domain.FieldBoolean:
		value, ok := parseBoolean(text)
		if !ok {
			return warning("invalid_boolean")
		}
		return domain.FieldValue{Type: field.FieldType, BooleanValue: &value}, nil
	case domain.FieldDateTime:
		value, err := parser.ParseTimestamp(text)
		if err != nil {
			return warning("invalid_datetime")
		}
		return domain.FieldValue{Type: field.FieldType, TimestampValue: &value}, nil
	case domain.FieldDuration:
		value, err := time.ParseDuration(text)
		if err != nil {
			return warning("invalid_duration")
		}
		return domain.FieldValue{Type: field.FieldType, DurationValue: &value}, nil
	default:
		return warning("unsupported_type")
	}
}

func validFieldType(fieldType domain.FieldType) bool {
	switch fieldType {
	case domain.FieldString, domain.FieldInteger, domain.FieldFloat,
		domain.FieldBoolean, domain.FieldDateTime, domain.FieldDuration:
		return true
	default:
		return false
	}
}

var fieldNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func validFieldName(value string) bool {
	return fieldNamePattern.MatchString(value)
}

func normalizeNamedCaptures(pattern string) string {
	return strings.ReplaceAll(pattern, "(?<", "(?P<")
}

func compileLiteralPattern(pattern string) (*regexp.Regexp, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	var expression strings.Builder
	expression.WriteString("^")
	for {
		start := strings.Index(pattern, "{")
		if start < 0 {
			expression.WriteString(regexp.QuoteMeta(pattern))
			break
		}
		end := strings.Index(pattern[start:], "}")
		if end < 0 {
			return nil, fmt.Errorf("literal pattern has an unclosed field")
		}
		end += start
		expression.WriteString(regexp.QuoteMeta(pattern[:start]))
		name := pattern[start+1 : end]
		if !validFieldName(name) {
			return nil, fmt.Errorf("invalid literal field %q", name)
		}
		expression.WriteString("(?P<" + name + ">.+?)")
		pattern = pattern[end+1:]
	}
	expression.WriteString("$")
	return regexp.Compile(expression.String())
}

func parseKeyValues(raw string) map[string]any {
	values := map[string]any{}
	pattern := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.-]*)=("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[^\s]+)`)
	for _, match := range pattern.FindAllStringSubmatch(raw, -1) {
		value := match[2]
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		values[match[1]] = value
	}
	return values
}

func flattenMapped(values map[string]any, mapping map[string]string) map[string]any {
	if len(mapping) == 0 {
		return values
	}
	result := make(map[string]any, len(mapping))
	for source, target := range mapping {
		if value, ok := values[source]; ok {
			result[target] = value
		}
	}
	return result
}

func stringMap(value any) map[string]string {
	raw, ok := value.(map[string]any)
	if !ok {
		if typed, ok := value.(map[string]string); ok {
			return typed
		}
		return map[string]string{}
	}
	result := make(map[string]string, len(raw))
	for key, item := range raw {
		if text, ok := item.(string); ok {
			result[key] = text
		}
	}
	return result
}

func parseBoolean(value string) (bool, bool) {
	switch strings.ToLower(value) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}
