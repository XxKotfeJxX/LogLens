import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  BarChart3,
  Calculator,
  Play,
  Save,
  Trash2,
} from "lucide-react";
import { Link, useParams } from "react-router-dom";
import {
  aggregateSource,
  createDerivedField,
  createSavedAnalysis,
  deleteDerivedField,
  deleteSavedAnalysis,
  getSource,
  getSourceFields,
  listDerivedFields,
  listSavedAnalyses,
  querySource,
  type AggregationRequest,
  type AggregationResult,
  type FieldType,
  type LogEntry,
  type SavedAnalysis,
  type TypedFilter,
} from "../api";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

const baseFields = [
  "event_type",
  "timestamp",
  "level",
  "service",
  "request_id",
  "parse_status",
  "entry_number",
];
const numericTypes: FieldType[] = ["integer", "float", "duration"];

export function Analyze() {
  const { sourceId = "" } = useParams();
  const client = useQueryClient();
  const source = useQuery({
    queryKey: ["source", sourceId],
    queryFn: () => getSource(sourceId),
  });
  const fields = useQuery({
    queryKey: ["source-fields", sourceId],
    queryFn: () => getSourceFields(sourceId),
  });
  const derived = useQuery({
    queryKey: ["derived-fields", source.data?.parserProfileId],
    queryFn: () => listDerivedFields(source.data!.parserProfileId!),
    enabled: Boolean(source.data?.parserProfileId),
  });
  const saved = useQuery({
    queryKey: ["saved-analyses", sourceId],
    queryFn: () => listSavedAnalyses(sourceId),
  });

  const allFields = useMemo(
    () => [
      ...baseFields,
      ...(fields.data ?? []).map((field) => field.name),
      ...(derived.data ?? []).map((field) => field.name),
    ],
    [fields.data, derived.data],
  );
  const measures = useMemo(
    () => [
      ...(fields.data ?? [])
        .filter(
          (field) =>
            field.aggregatable && numericTypes.includes(field.fieldType),
        )
        .map((field) => field.name),
      ...(derived.data ?? [])
        .filter((field) => numericTypes.includes(field.resultType))
        .map((field) => field.name),
    ],
    [fields.data, derived.data],
  );

  const [filterField, setFilterField] = useState("event_type");
  const [operator, setOperator] = useState<TypedFilter["operator"]>("eq");
  const [filterValue, setFilterValue] = useState("");
  const [operation, setOperation] =
    useState<AggregationRequest["operation"]>("count");
  const [measure, setMeasure] = useState("");
  const [groupBy, setGroupBy] = useState("");
  const [chartType, setChartType] = useState<SavedAnalysis["chartType"]>("bar");
  const [rows, setRows] = useState<LogEntry[]>([]);
  const [aggregation, setAggregation] = useState<AggregationResult | null>(
    null,
  );
  const [error, setError] = useState("");
  const [derivedName, setDerivedName] = useState("");
  const [derivedType, setDerivedType] = useState<FieldType>("float");
  const [expression, setExpression] = useState("");
  const [analysisName, setAnalysisName] = useState("");

  const filters: TypedFilter[] = filterValue.trim()
    ? [{ field: filterField, operator, value: parseFilterValue(filterValue) }]
    : [];
  const configuration = {
    operation,
    field: operation === "count" ? "" : measure,
    groupBy,
    filters,
  };

  const runQuery = useMutation({
    mutationFn: () =>
      querySource(sourceId, {
        filters,
        sort: [{ field: "entry_number", direction: "desc" }],
        limit: 200,
      }),
    onSuccess: (result) => {
      setRows(result.items);
      setAggregation(null);
      setError("");
    },
    onError: (failure) => setError(failure.message),
  });
  const runAggregation = useMutation({
    mutationFn: () =>
      aggregateSource(sourceId, configuration as AggregationRequest),
    onSuccess: (result) => {
      setAggregation(result);
      setRows([]);
      setError("");
    },
    onError: (failure) => setError(failure.message),
  });
  const createDerived = useMutation({
    mutationFn: () =>
      createDerivedField(source.data!.parserProfileId!, {
        name: derivedName.trim(),
        resultType: derivedType,
        expression: expression.trim(),
      }),
    onSuccess: () => {
      setDerivedName("");
      setExpression("");
      setError("");
      void client.invalidateQueries({
        queryKey: ["derived-fields", source.data?.parserProfileId],
      });
    },
    onError: (failure) => setError(failure.message),
  });
  const saveAnalysis = useMutation({
    mutationFn: () =>
      createSavedAnalysis(sourceId, {
        name: analysisName.trim(),
        configuration,
        chartType,
      }),
    onSuccess: () => {
      setAnalysisName("");
      setError("");
      void client.invalidateQueries({ queryKey: ["saved-analyses", sourceId] });
    },
    onError: (failure) => setError(failure.message),
  });

  function loadAnalysis(item: SavedAnalysis) {
    const config = item.configuration;
    setOperation(
      (config.operation as AggregationRequest["operation"]) ?? "count",
    );
    setMeasure(String(config.field ?? ""));
    setGroupBy(String(config.groupBy ?? ""));
    setChartType(item.chartType);
    const savedFilters = config.filters as TypedFilter[] | undefined;
    if (savedFilters?.[0]) {
      setFilterField(savedFilters[0].field);
      setOperator(savedFilters[0].operator);
      setFilterValue(String(savedFilters[0].value ?? ""));
    }
  }

  if (source.isLoading)
    return <div className="page loading-page">Opening analysis workspace…</div>;
  if (!source.data)
    return (
      <div className="page">
        <div className="error-state">Source not found</div>
      </div>
    );

  return (
    <section className="page analyze-page">
      <div className="studio-titlebar">
        <Link
          to={`/sources/${sourceId}`}
          className="icon-button"
          aria-label="Back to logs"
        >
          <ArrowLeft size={18} />
        </Link>
        <div>
          <span className="eyebrow">v0.5 · Structured Analysis</span>
          <h1>{source.data.name}</h1>
          <p className="lede">
            Typed filters, bounded aggregations, derived fields, and reusable
            investigations.
          </p>
        </div>
      </div>
      {error ? <div className="error-state compact">{error}</div> : null}

      <div className="analysis-tabs">
        <Link to={`/sources/${sourceId}`}>Logs</Link>
        <Link to={`/sources/${sourceId}/extraction`}>Fields & Rules</Link>
        <span className="active">Analyze</span>
      </div>

      <div className="analysis-grid">
        <div className="panel analysis-builder">
          <div className="panel-heading">
            <div>
              <h2>Query builder</h2>
              <p>Filters preserve field types and use indexed values.</p>
            </div>
          </div>
          <div className="analysis-form-row">
            <label className="form-field">
              <span>Field</span>
              <select
                value={filterField}
                onChange={(event) => setFilterField(event.target.value)}
              >
                {allFields.map((field) => (
                  <option key={field}>{field}</option>
                ))}
              </select>
            </label>
            <label className="form-field">
              <span>Operator</span>
              <select
                value={operator}
                onChange={(event) =>
                  setOperator(event.target.value as TypedFilter["operator"])
                }
              >
                {["eq", "ne", "gt", "gte", "lt", "lte", "contains"].map(
                  (value) => (
                    <option key={value}>{value}</option>
                  ),
                )}
              </select>
            </label>
            <label className="form-field">
              <span>Value</span>
              <input
                value={filterValue}
                onChange={(event) => setFilterValue(event.target.value)}
                placeholder="optional"
              />
            </label>
            <button
              className="button"
              onClick={() => runQuery.mutate()}
              disabled={runQuery.isPending}
            >
              <Play size={15} /> Query rows
            </button>
          </div>
          <div className="analysis-form-row aggregation-row">
            <label className="form-field">
              <span>Aggregation</span>
              <select
                value={operation}
                onChange={(event) =>
                  setOperation(
                    event.target.value as AggregationRequest["operation"],
                  )
                }
              >
                {["count", "sum", "avg", "min", "max"].map((value) => (
                  <option key={value}>{value}</option>
                ))}
              </select>
            </label>
            <label className="form-field">
              <span>Measure</span>
              <select
                value={measure}
                disabled={operation === "count"}
                onChange={(event) => setMeasure(event.target.value)}
              >
                <option value="">Select numeric field</option>
                {measures.map((field) => (
                  <option key={field}>{field}</option>
                ))}
              </select>
            </label>
            <label className="form-field">
              <span>Group by</span>
              <select
                value={groupBy}
                onChange={(event) => setGroupBy(event.target.value)}
              >
                <option value="">No grouping</option>
                {allFields.map((field) => (
                  <option key={field}>{field}</option>
                ))}
              </select>
            </label>
            <button
              className="button button-primary"
              onClick={() => runAggregation.mutate()}
              disabled={
                runAggregation.isPending || (operation !== "count" && !measure)
              }
            >
              <BarChart3 size={15} /> Run analysis
            </button>
          </div>
        </div>

        <aside className="panel analysis-side">
          <h2>
            <Calculator size={17} /> Derived fields
          </h2>
          {source.data.parserProfileId ? (
            <>
              <div className="derived-list">
                {derived.data?.map((field) => (
                  <div key={field.id}>
                    <span>
                      <strong>{field.name}</strong>
                      <small>
                        {field.expression} → {field.resultType}
                      </small>
                    </span>
                    <button
                      className="icon-button"
                      onClick={() =>
                        void deleteDerivedField(
                          source.data!.parserProfileId!,
                          field.id,
                        ).then(() =>
                          client.invalidateQueries({
                            queryKey: [
                              "derived-fields",
                              source.data?.parserProfileId,
                            ],
                          }),
                        )
                      }
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                ))}
              </div>
              <label className="form-field">
                <span>Name</span>
                <input
                  value={derivedName}
                  onChange={(event) => setDerivedName(event.target.value)}
                  placeholder="dps"
                />
              </label>
              <label className="form-field">
                <span>Result type</span>
                <select
                  value={derivedType}
                  onChange={(event) =>
                    setDerivedType(event.target.value as FieldType)
                  }
                >
                  {["string", "integer", "float", "datetime", "duration"].map(
                    (value) => (
                      <option key={value}>{value}</option>
                    ),
                  )}
                </select>
              </label>
              <label className="form-field">
                <span>Safe expression</span>
                <input
                  value={expression}
                  onChange={(event) => setExpression(event.target.value)}
                  placeholder="damage / duration"
                />
              </label>
              <button
                className="button"
                disabled={
                  !derivedName.trim() ||
                  !expression.trim() ||
                  createDerived.isPending
                }
                onClick={() => createDerived.mutate()}
              >
                Add derived field
              </button>
            </>
          ) : (
            <p className="subtle">
              Assign a parser profile before adding derived fields.
            </p>
          )}
        </aside>

        <div className="panel analysis-results">
          <div className="panel-heading">
            <div>
              <h2>Results</h2>
              <p>
                {aggregation
                  ? `${aggregation.operation.toUpperCase()} · ${aggregation.buckets.length} buckets`
                  : `${rows.length} matching rows`}
              </p>
            </div>
          </div>
          {aggregation ? (
            <AggregationView result={aggregation} chartType={chartType} />
          ) : (
            <ResultRows rows={rows} />
          )}
        </div>

        <aside className="panel analysis-side saved-side">
          <h2>
            <Save size={17} /> Saved analyses
          </h2>
          <div className="save-row">
            <input
              value={analysisName}
              onChange={(event) => setAnalysisName(event.target.value)}
              placeholder="Damage by weapon"
            />
            <select
              value={chartType}
              onChange={(event) =>
                setChartType(event.target.value as SavedAnalysis["chartType"])
              }
            >
              <option>bar</option>
              <option>line</option>
              <option>table</option>
            </select>
            <button
              className="button"
              disabled={!analysisName.trim()}
              onClick={() => saveAnalysis.mutate()}
            >
              Save
            </button>
          </div>
          <div className="saved-list">
            {saved.data?.map((item) => (
              <div key={item.id}>
                <button onClick={() => loadAnalysis(item)}>
                  <strong>{item.name}</strong>
                  <small>
                    {item.chartType} ·{" "}
                    {new Date(item.updatedAt).toLocaleDateString()}
                  </small>
                </button>
                <button
                  className="icon-button"
                  onClick={() =>
                    void deleteSavedAnalysis(sourceId, item.id).then(() =>
                      client.invalidateQueries({
                        queryKey: ["saved-analyses", sourceId],
                      }),
                    )
                  }
                >
                  <Trash2 size={14} />
                </button>
              </div>
            ))}
          </div>
        </aside>
      </div>
    </section>
  );
}

function AggregationView({
  result,
  chartType,
}: {
  result: AggregationResult;
  chartType: SavedAnalysis["chartType"];
}) {
  const data = result.buckets.map((bucket, index) => ({
    group: String(bucket.group ?? `Total ${index + 1}`),
    value: bucket.value,
  }));
  if (chartType === "table")
    return (
      <div className="analysis-table">
        {data.map((row) => (
          <div key={row.group}>
            <span>{row.group}</span>
            <strong>{row.value.toLocaleString()}</strong>
          </div>
        ))}
      </div>
    );
  return (
    <div className="analysis-chart">
      <ResponsiveContainer width="100%" height="100%">
        {chartType === "line" ? (
          <LineChart data={data}>
            <CartesianGrid strokeDasharray="3 3" />
            <XAxis dataKey="group" />
            <YAxis />
            <Tooltip />
            <Line dataKey="value" stroke="#475f8f" strokeWidth={2} />
          </LineChart>
        ) : (
          <BarChart data={data}>
            <CartesianGrid strokeDasharray="3 3" />
            <XAxis dataKey="group" />
            <YAxis />
            <Tooltip />
            <Bar dataKey="value" fill="#475f8f" />
          </BarChart>
        )}
      </ResponsiveContainer>
    </div>
  );
}

function ResultRows({ rows }: { rows: LogEntry[] }) {
  if (!rows.length)
    return (
      <div className="table-message">Run a query to inspect typed results.</div>
    );
  return (
    <div className="query-results">
      {rows.map((row) => (
        <div key={row.number}>
          <span>#{row.number}</span>
          <strong>{row.eventType || row.level}</strong>
          <code>{row.message}</code>
          <small>{JSON.stringify(row.fields ?? {})}</small>
        </div>
      ))}
    </div>
  );
}

function parseFilterValue(value: string): unknown {
  if (value === "true") return true;
  if (value === "false") return false;
  const number = Number(value);
  return value.trim() !== "" && Number.isFinite(number) ? number : value;
}
