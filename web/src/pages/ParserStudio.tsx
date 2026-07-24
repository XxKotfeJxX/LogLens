import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  CheckCircle2,
  FlaskConical,
  Plus,
  Save,
  Trash2,
} from "lucide-react";
import { Link, useParams } from "react-router-dom";
import {
  createParserVersion,
  getParserProfile,
  listSources,
  previewParserProfile,
  type FieldType,
  type PreviewResult,
} from "../api";

interface RuleDraft {
  key: string;
  name: string;
  eventType: string;
  matcherType: "regex" | "literal_pattern" | "key_value" | "json";
  pattern: string;
}

interface FieldDraft {
  key: string;
  name: string;
  fieldType: FieldType;
  indexed: boolean;
  aggregatable: boolean;
  nullable: boolean;
}

const fieldTypes: FieldType[] = [
  "string",
  "integer",
  "float",
  "boolean",
  "datetime",
  "duration",
];

function newRule(): RuleDraft {
  return {
    key: crypto.randomUUID(),
    name: "",
    eventType: "",
    matcherType: "regex",
    pattern: "",
  };
}

function newField(): FieldDraft {
  return {
    key: crypto.randomUUID(),
    name: "",
    fieldType: "string",
    indexed: false,
    aggregatable: false,
    nullable: true,
  };
}

export function ParserStudio() {
  const { profileId = "" } = useParams();
  const client = useQueryClient();
  const [rules, setRules] = useState<RuleDraft[]>([newRule()]);
  const [fields, setFields] = useState<FieldDraft[]>([newField()]);
  const [sourceId, setSourceId] = useState("");
  const [preview, setPreview] = useState<PreviewResult | null>(null);
  const profile = useQuery({
    queryKey: ["parser-profile", profileId],
    queryFn: () => getParserProfile(profileId),
  });
  const sources = useQuery({ queryKey: ["sources"], queryFn: listSources });
  const latest = profile.data?.versions[0];
  const create = useMutation({
    mutationFn: () =>
      createParserVersion(profileId, {
        recordFormat: "plain_text",
        configuration: { allowMultiple: false },
        rules: rules.map((rule, index) => ({
          name: rule.name,
          eventType: rule.eventType,
          priority: index + 1,
          matcherType: rule.matcherType,
          matcherConfig:
            rule.matcherType === "regex" ||
            rule.matcherType === "literal_pattern"
              ? { pattern: rule.pattern }
              : {},
          enabled: true,
        })),
        fields: fields.map((field) => ({
          name: field.name,
          fieldType: field.fieldType,
          indexed: field.indexed,
          aggregatable: field.aggregatable,
          nullable: field.nullable,
        })),
      }),
    onSuccess: () => {
      setPreview(null);
      void client.invalidateQueries({
        queryKey: ["parser-profile", profileId],
      });
    },
  });
  const runPreview = useMutation({
    mutationFn: () =>
      previewParserProfile(profileId, sourceId, latest?.version),
    onSuccess: setPreview,
  });
  const formReady = useMemo(
    () =>
      rules.length > 0 &&
      rules.every(
        (rule) =>
          rule.name.trim() &&
          rule.eventType.trim() &&
          (!["regex", "literal_pattern"].includes(rule.matcherType) ||
            rule.pattern.trim()),
      ) &&
      fields.length > 0 &&
      fields.every((field) => field.name.trim()),
    [rules, fields],
  );

  if (profile.isLoading) {
    return <div className="page loading-page">Opening parser studio…</div>;
  }
  if (!profile.data || profile.isError) {
    return (
      <div className="page error-state">
        {profile.error?.message ?? "Parser profile not found"}
      </div>
    );
  }

  return (
    <section className="page studio-page">
      <div className="studio-titlebar">
        <Link to="/parsers" className="icon-button" aria-label="Back">
          <ArrowLeft size={17} />
        </Link>
        <div>
          <div className="eyebrow">Extraction Studio</div>
          <h1>{profile.data.profile.name}</h1>
          <p>
            {profile.data.profile.description || "Versioned parser profile"}
          </p>
        </div>
        <div className="version-pill">
          {latest ? `Latest v${latest.version}` : "No versions"}
        </div>
      </div>

      <div className="builder-layout">
        <section className="panel builder-panel">
          <div className="panel-heading">
            <div>
              <h2>Event rules</h2>
              <p>First matching rule wins; drag-free order is the priority.</p>
            </div>
            <button
              className="button button-small"
              onClick={() => setRules((items) => [...items, newRule()])}
            >
              <Plus size={14} /> Rule
            </button>
          </div>
          <div className="rule-stack">
            {rules.map((rule, index) => (
              <div className="rule-card" key={rule.key}>
                <span className="priority-badge">{index + 1}</span>
                <input
                  aria-label={`Rule ${index + 1} name`}
                  value={rule.name}
                  onChange={(event) =>
                    setRules((items) =>
                      items.map((item) =>
                        item.key === rule.key
                          ? { ...item, name: event.target.value }
                          : item,
                      ),
                    )
                  }
                  placeholder="attack"
                />
                <input
                  aria-label={`Rule ${index + 1} event type`}
                  value={rule.eventType}
                  onChange={(event) =>
                    setRules((items) =>
                      items.map((item) =>
                        item.key === rule.key
                          ? { ...item, eventType: event.target.value }
                          : item,
                      ),
                    )
                  }
                  placeholder="event_type"
                />
                <select
                  value={rule.matcherType}
                  onChange={(event) =>
                    setRules((items) =>
                      items.map((item) =>
                        item.key === rule.key
                          ? {
                              ...item,
                              matcherType: event.target
                                .value as RuleDraft["matcherType"],
                            }
                          : item,
                      ),
                    )
                  }
                >
                  <option value="regex">Advanced regex</option>
                  <option value="literal_pattern">Guided pattern</option>
                  <option value="key_value">Key-value</option>
                  <option value="json">JSON</option>
                </select>
                {rule.matcherType === "regex" ||
                rule.matcherType === "literal_pattern" ? (
                  <textarea
                    aria-label={`Rule ${index + 1} pattern`}
                    value={rule.pattern}
                    onChange={(event) =>
                      setRules((items) =>
                        items.map((item) =>
                          item.key === rule.key
                            ? { ...item, pattern: event.target.value }
                            : item,
                        ),
                      )
                    }
                    placeholder={
                      rule.matcherType === "regex"
                        ? String.raw`(?<player>\w+) attacked (?<target>\w+) for (?<damage>\d+) damage`
                        : "{player} attacked {target} for {damage} damage"
                    }
                  />
                ) : (
                  <span className="subtle">
                    Fields are discovered from the record and mapped by matching
                    field definitions.
                  </span>
                )}
                <button
                  className="icon-button danger"
                  disabled={rules.length === 1}
                  onClick={() =>
                    setRules((items) =>
                      items.filter((item) => item.key !== rule.key),
                    )
                  }
                  aria-label={`Remove rule ${index + 1}`}
                >
                  <Trash2 size={14} />
                </button>
              </div>
            ))}
          </div>
        </section>

        <section className="panel builder-panel">
          <div className="panel-heading">
            <div>
              <h2>Typed fields</h2>
              <p>Only indexed or aggregatable fields get dedicated rows.</p>
            </div>
            <button
              className="button button-small"
              onClick={() => setFields((items) => [...items, newField()])}
            >
              <Plus size={14} /> Field
            </button>
          </div>
          <div className="field-stack">
            {fields.map((field) => (
              <div className="field-row" key={field.key}>
                <input
                  value={field.name}
                  onChange={(event) =>
                    setFields((items) =>
                      items.map((item) =>
                        item.key === field.key
                          ? { ...item, name: event.target.value }
                          : item,
                      ),
                    )
                  }
                  placeholder="damage"
                />
                <select
                  value={field.fieldType}
                  onChange={(event) =>
                    setFields((items) =>
                      items.map((item) =>
                        item.key === field.key
                          ? {
                              ...item,
                              fieldType: event.target.value as FieldType,
                            }
                          : item,
                      ),
                    )
                  }
                >
                  {fieldTypes.map((type) => (
                    <option key={type}>{type}</option>
                  ))}
                </select>
                {(["indexed", "aggregatable"] as const).map((flag) => (
                  <label className="check-field" key={flag}>
                    <input
                      type="checkbox"
                      checked={field[flag]}
                      onChange={(event) =>
                        setFields((items) =>
                          items.map((item) =>
                            item.key === field.key
                              ? { ...item, [flag]: event.target.checked }
                              : item,
                          ),
                        )
                      }
                    />
                    {flag}
                  </label>
                ))}
                <button
                  className="icon-button danger"
                  disabled={fields.length === 1}
                  onClick={() =>
                    setFields((items) =>
                      items.filter((item) => item.key !== field.key),
                    )
                  }
                  aria-label={`Remove ${field.name || "field"}`}
                >
                  <Trash2 size={14} />
                </button>
              </div>
            ))}
          </div>
          {create.isError ? (
            <div className="error-state compact">{create.error.message}</div>
          ) : null}
          <button
            className="button button-primary"
            disabled={!formReady || create.isPending}
            onClick={() => create.mutate()}
          >
            <Save size={15} /> Save immutable version
          </button>
        </section>

        <section className="panel preview-panel">
          <div className="panel-heading">
            <div>
              <h2>Extraction preview</h2>
              <p>
                Runs against up to 500 indexed entries without modifying them.
              </p>
            </div>
          </div>
          <div className="preview-controls">
            <select
              value={sourceId}
              onChange={(event) => setSourceId(event.target.value)}
            >
              <option value="">Choose a source</option>
              {sources.data
                ?.filter((source) => source.entryCount > 0)
                .map((source) => (
                  <option value={source.id} key={source.id}>
                    {source.name}
                  </option>
                ))}
            </select>
            <button
              className="button"
              disabled={!latest || !sourceId || runPreview.isPending}
              onClick={() => runPreview.mutate()}
            >
              <FlaskConical size={15} /> Preview v{latest?.version ?? "—"}
            </button>
          </div>
          {preview ? <PreviewTable preview={preview} /> : null}
          {runPreview.isError ? (
            <div className="error-state compact">
              {runPreview.error.message}
            </div>
          ) : null}
        </section>
      </div>
    </section>
  );
}

function PreviewTable({ preview }: { preview: PreviewResult }) {
  return (
    <>
      <div className="preview-metrics">
        <div>
          <span>Coverage</span>
          <strong>{(preview.coverage * 100).toFixed(1)}%</strong>
        </div>
        <div>
          <span>Matched</span>
          <strong>{preview.matched}</strong>
        </div>
        <div>
          <span>Unmatched</span>
          <strong>{preview.unmatched}</strong>
        </div>
        <div>
          <span>Type errors</span>
          <strong>{preview.typeErrors}</strong>
        </div>
      </div>
      <div className="preview-table">
        {preview.items.slice(0, 30).map((item) => (
          <div className="preview-row" key={item.number}>
            <span>#{item.number}</span>
            <code>{item.raw}</code>
            <strong>
              {item.eventType ? (
                <>
                  <CheckCircle2 size={13} /> {item.eventType}
                </>
              ) : (
                "unmatched"
              )}
            </strong>
            <pre>{JSON.stringify(item.fields)}</pre>
          </div>
        ))}
      </div>
    </>
  );
}
