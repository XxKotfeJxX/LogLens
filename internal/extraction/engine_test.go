package extraction

import (
	"testing"

	"loglens/internal/domain"
	"loglens/internal/parser"
)

func TestRegexExtractionUsesPriorityAndTypedFields(t *testing.T) {
	version := domain.ParserProfileVersion{
		Version: 1,
		Fields: []domain.FieldDefinition{
			{Name: "player", FieldType: domain.FieldString, Nullable: false},
			{Name: "target", FieldType: domain.FieldString, Nullable: false},
			{Name: "damage", FieldType: domain.FieldInteger, Indexed: true, Aggregatable: true, Nullable: false},
		},
		Rules: []domain.ExtractionRule{
			{
				ID: "generic", Name: "generic", EventType: "message", Priority: 20,
				MatcherType: "regex", MatcherConfig: map[string]any{"pattern": `(?<player>\w+) attacked .+`},
				Enabled: true,
			},
			{
				ID: "attack", Name: "attack", EventType: "attack", Priority: 10,
				MatcherType: "regex",
				MatcherConfig: map[string]any{
					"pattern": `(?<player>\w+) attacked (?<target>\w+) for (?<damage>\d+) damage`,
				},
				Enabled: true,
			},
		},
	}
	engine, err := Compile(version)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("Andrew attacked Goblin for 42 damage")
	result := engine.Extract(raw, parser.Parse("plain_text", raw))
	if result.EventType != "attack" || result.MatchedRuleID == nil || *result.MatchedRuleID != "attack" {
		t.Fatalf("unexpected match: %+v", result)
	}
	if value := result.Fields["damage"].IntegerValue; value == nil || *value != 42 {
		t.Fatalf("damage was not converted: %+v", result.Fields["damage"])
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "ambiguous_rule" {
		t.Fatalf("expected an ambiguity warning, got %+v", result.Warnings)
	}
}

func TestDisabledRulesAndInvalidConversion(t *testing.T) {
	version := domain.ParserProfileVersion{
		Fields: []domain.FieldDefinition{
			{Name: "damage", FieldType: domain.FieldInteger, Nullable: false},
		},
		Rules: []domain.ExtractionRule{
			{
				ID: "disabled", Name: "disabled", EventType: "ignored", Priority: 1,
				MatcherType: "regex", MatcherConfig: map[string]any{"pattern": `(?<damage>.+)`},
				Enabled: false,
			},
			{
				ID: "active", Name: "active", EventType: "attack", Priority: 2,
				MatcherType: "regex", MatcherConfig: map[string]any{"pattern": `damage=(?<damage>\w+)`},
				Enabled: true,
			},
		},
	}
	engine, err := Compile(version)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("damage=unknown")
	result := engine.Extract(raw, parser.Parse("plain_text", raw))
	if result.EventType != "attack" {
		t.Fatalf("disabled rule executed: %+v", result)
	}
	if _, exists := result.Fields["damage"]; exists {
		t.Fatal("invalid integer must not be stored as a typed value")
	}
	if len(result.Warnings) != 2 {
		t.Fatalf("expected conversion and required warnings, got %+v", result.Warnings)
	}
}

func TestInvalidRegexIsRejected(t *testing.T) {
	_, err := Compile(domain.ParserProfileVersion{
		Rules: []domain.ExtractionRule{{
			Name: "broken", EventType: "broken", MatcherType: "regex",
			MatcherConfig: map[string]any{"pattern": `(?<value>`}, Enabled: true,
		}},
	})
	if err == nil {
		t.Fatal("invalid regex was accepted")
	}
}

func BenchmarkCompiledRegexExtraction(b *testing.B) {
	engine, err := Compile(domain.ParserProfileVersion{
		Version: 1,
		Fields: []domain.FieldDefinition{
			{Name: "player", FieldType: domain.FieldString, Indexed: true},
			{Name: "target", FieldType: domain.FieldString, Indexed: true},
			{Name: "damage", FieldType: domain.FieldInteger, Indexed: true, Aggregatable: true},
		},
		Rules: []domain.ExtractionRule{{
			ID: "attack", Name: "attack", EventType: "attack", Priority: 1,
			MatcherType: "regex", Enabled: true,
			MatcherConfig: map[string]any{
				"pattern": `(?<player>\w+) attacked (?<target>\w+) for (?<damage>\d+) damage`,
			},
		}},
	})
	if err != nil {
		b.Fatal(err)
	}
	raw := []byte("Andrew attacked Goblin for 42 damage")
	envelope := parser.Parse("plain_text", raw)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for range b.N {
		result := engine.Extract(raw, envelope)
		if result.EventType != "attack" {
			b.Fatal("rule did not match")
		}
	}
}
