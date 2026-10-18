import { useRef, useState } from "react";
import { api } from "./api/client";
import type { components } from "./api/schema";

type Catalog = components["schemas"]["CatalogAvailability"];
const reasons = {
  missing_table: "Table is missing or not visible to the warehouse account.",
  missing_column: "An approved column is missing.",
  incompatible_type: "A column type does not match the catalog.",
  access_denied: "The warehouse account cannot inspect this table.",
  scan_failed: "The metadata scan failed. Check the warehouse connection and run another scan.",
  not_scanned: "No scan exists for the current catalog. Run metadata ingestion.",
  stale: "The metadata scan has expired. Run metadata ingestion.",
};
const timestamp = (value?: string) => value ? new Date(value).toLocaleString() : "Never";

export default function App() {
  const token = useRef<HTMLInputElement>(null);
  const [catalog, setCatalog] = useState<Catalog>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  async function load(event: React.SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    setLoading(true); setError(""); setCatalog(undefined);
    try {
      const { data, response } = await api.GET("/api/v1/catalog", {
        headers: { Authorization: `Bearer ${token.current?.value ?? ""}` },
      });
      if (data) setCatalog(data);
      else setError(response.status === 401 ? "Catalog access denied. Enter a valid operator token." : "Catalog availability could not be loaded. Try again.");
    } catch { setError("Cannot reach the service. Try again."); }
    finally { setLoading(false); }
  }
  return (
    <main className="min-h-screen bg-slate-950 px-6 py-12 text-slate-100">
      <div className="mx-auto max-w-3xl">
        <p className="text-sm font-semibold uppercase tracking-widest text-cyan-400">Tesseract</p>
        <h1 className="mt-6 text-3xl font-semibold">Catalog availability</h1>
        <p className="mt-3 text-slate-300">See which approved warehouse tables are ready for analysis.</p>
        <form onSubmit={load} className="my-8 flex flex-wrap items-end gap-3">
          <label className="flex flex-col gap-2 text-sm">Operator token
            <input ref={token} type="password" required autoComplete="off" className="rounded border border-slate-600 bg-slate-900 px-3 py-2" />
          </label>
          <button disabled={loading} className="rounded bg-cyan-400 px-4 py-2 font-medium text-slate-950 disabled:opacity-50">{loading ? "Loading…" : "Load / refresh"}</button>
        </form>
        {error && <p role="alert" className="mb-6 text-amber-300">{error}</p>}
        <div aria-live="polite" aria-busy={loading}>
          {catalog?.sources.map(source => (
            <section key={source.source_id} className="mb-6 rounded-xl border border-slate-700 p-5">
              <h2 className="text-xl font-semibold">{source.source_id}</h2>
              <p className="mt-3 text-lg">{source.configured} configured / {source.available} available / {source.unavailable} unavailable</p>
              <p className="mt-2 text-sm text-slate-300">{source.stale} stale (included in unavailable)</p>
              <p className="mt-3 text-sm text-slate-400">Last scan: {timestamp(source.last_scan)}<br />Last successful scan: {timestamp(source.last_successful_scan)}</p>
              <ul className="mt-5 divide-y divide-slate-700">
                {source.tables.map(table => <li key={table.dataset_id} className="py-4">
                  <div className="flex flex-wrap justify-between gap-2"><span className="font-medium">{table.schema}.{table.table}</span><span className={table.status === "available" ? "text-emerald-300" : "text-amber-300"}>{table.status}{table.stale ? " · stale" : ""}</span></div>
                  {table.reason && <p className="mt-1 text-sm text-slate-300">{reasons[table.reason]}</p>}
                  <p className="mt-1 text-xs text-slate-400">Inspected: {timestamp(table.inspected_at)}</p>
                </li>)}
              </ul>
            </section>
          ))}
        </div>
      </div>
    </main>
  );
}
