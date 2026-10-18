import { lazy, Suspense, useEffect, useState } from "react";
import { api } from "../api/client";
import { cell, failure, widgetQuery, type Result, type Spec, type Widget } from "./model";
import ChartBoundary from "./ChartBoundary";
const Chart = lazy(() => import("./Chart"));
const warnings: Record<string, string> = {
  denied: "Denied: your data permissions do not allow this widget.",
  unavailable: "Source unavailable: refresh catalog metadata or contact your administrator.",
  invalid: "Invalid widget: its query no longer matches the approved semantic catalog.",
};
export function DataTable({result}: {result: Result}) {
  return <div className="overflow-auto max-h-96"><table className="w-full text-left text-sm"><thead><tr>{result.columns.map(c => <th key={c} className="p-2 border-b border-slate-600">{c}</th>)}</tr></thead><tbody>{result.rows.map((row, i) => <tr key={i}>{result.columns.map((c, j) => <td key={c} className="p-2 border-b border-slate-800">{cell(row[j])}</td>)}</tr>)}</tbody></table></div>;
}
export default function WidgetView({widget, filters, status, onSessionExpired}: {widget: Widget; filters: Spec["filters"]; status: string; onSessionExpired?: () => void}) {
  const [result, setResult] = useState<Result>();
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setResult(undefined); setError("");
    if (status !== "ready") return () => controller.abort();
    async function run() {
      try {
        const {data, error, response} = await api.POST("/api/v1/queries", {body: widgetQuery(widget, filters), signal: controller.signal});
        if (controller.signal.aborted) return;
        if (response.status === 401) onSessionExpired?.();
        if (data) setResult(data); else setError(failure(response.status, error?.error.code));
      } catch { if (!controller.signal.aborted) setError("Query failed: cannot reach the service."); }
    }
    void run();
    return () => controller.abort();
  }, [widget, filters, status, attempt, onSessionExpired]);
  if (status !== "ready") return <p role="alert">{warnings[status] ?? "Widget validation is missing. Reload the dashboard."}</p>;
  if (error) return <div role="alert"><p>{error}</p><button onClick={() => setAttempt(a => a + 1)}>Retry query</button></div>;
  if (!result) return <p role="status">Loading live data…</p>;
  if (result.rows.length === 0) return <p>No data matches these filters.</p>;
  return <>
    {widget.kind === "kpi" ? <p className="my-6 text-4xl font-semibold text-cyan-300">{cell(result.rows[0][result.columns.indexOf(widget.query.metrics[0])])}</p> : widget.kind === "table" ? <DataTable result={result} /> : <><ChartBoundary><Suspense fallback={<p>Loading chart…</p>}><Chart widget={widget} result={result} /></Suspense></ChartBoundary><details><summary>View exact values</summary><DataTable result={result} /></details></>}
    {result.rows.length >= widget.query.limit && <p className="mt-3 text-amber-300 text-sm">Result limit reached ({widget.query.limit}); more groups may exist. Narrow the filters.</p>}
  </>;
}
