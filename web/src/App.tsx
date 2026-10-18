import { useEffect, useState } from "react";
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
  const [session, setSession] = useState<components["schemas"]["Session"]>();
  const [checking, setChecking] = useState(true);
  const [changing, setChanging] = useState(false);
  useEffect(() => { api.GET("/api/v1/auth/session").then(({data}) => setSession(data)).catch(() => setError("Cannot reach the service.")).finally(() => setChecking(false)); }, []);
  async function login(event: React.SubmitEvent<HTMLFormElement>) {
    event.preventDefault(); const form = event.currentTarget; const values = new FormData(form);
    setLoading(true); setError("");
    try {
      const {data, response} = await api.POST("/api/v1/auth/login", {body: {email: String(values.get("email")), password: String(values.get("password"))}});
      if (data) { setSession(data); form.reset(); }
      else setError(response.status === 429 ? "Too many attempts. Try again in 15 minutes." : "Sign-in failed. Check your email and password.");
    } catch { setError("Cannot reach the service."); } finally { setLoading(false); }
  }
  async function logout() {
    setLoading(true); setError("");
    try { const {response} = await api.POST("/api/v1/auth/logout");
      if (response.ok || response.status === 401) {setSession(undefined);setCatalog(undefined);setChanging(false);} else setError("Sign-out failed. Try again.");
    } catch {setError("Cannot reach the service.");} finally {setLoading(false);}
  }
  async function changePassword(event: React.SubmitEvent<HTMLFormElement>) {
    event.preventDefault(); const values = new FormData(event.currentTarget); setLoading(true);setError("");
    try { const {response} = await api.POST("/api/v1/auth/password", {body: {current_password: String(values.get("current")), new_password: String(values.get("next"))}});
      if(response.ok){setSession(undefined);setCatalog(undefined);setChanging(false);setError("Password changed. Sign in again.");}
      else setError(response.status === 429 ? "Too many attempts. Try again later." : "Password change failed. Check your current password.");
    } catch {setError("Cannot reach the service.");} finally {setLoading(false);}
  }
  const [catalog, setCatalog] = useState<Catalog>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  async function load(event: React.SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    setLoading(true); setError(""); setCatalog(undefined);
    try {
      const { data, response } = await api.GET("/api/v1/catalog");
      if (data) setCatalog(data);
      else if (response.status === 401) {setSession(undefined);setChanging(false);setError("Your session expired. Sign in again.");}
      else setError("Catalog availability could not be loaded. Try again.");
    } catch { setError("Cannot reach the service. Try again."); }
    finally { setLoading(false); }
  }
  return (
    <main className="min-h-screen bg-slate-950 px-6 py-12 text-slate-100">
      <div className="mx-auto max-w-3xl">
        <p className="text-sm font-semibold uppercase tracking-widest text-cyan-400">Tesseract</p>
        {checking ? <p className="mt-6">Checking session…</p> : !session ? <>
          <h1 className="mt-6 text-3xl font-semibold">Sign in</h1>
          <p className="mt-3 text-slate-300">Use the account provided by your administrator. Contact them if you need access or a password reset.</p>
          <form onSubmit={login} className="my-8 grid gap-4 max-w-sm">
            <label>Email<input name="email" type="email" required autoComplete="username" maxLength={254} className="block w-full rounded bg-slate-800 p-2" /></label>
            <label>Password<input name="password" type="password" required autoComplete="current-password" maxLength={1024} className="block w-full rounded bg-slate-800 p-2" /></label>
            <button disabled={loading} className="rounded bg-cyan-400 px-4 py-2 text-slate-950 disabled:opacity-50">{loading ? "Signing in…" : "Sign in"}</button>
          </form>
        </> : <>
          <div className="mt-6 flex flex-wrap gap-4 items-center"><span>{session.email}</span><button disabled={loading} onClick={logout}>Sign out</button><button onClick={() => setChanging(!changing)}>Change password</button></div>
          {changing && <form onSubmit={changePassword} className="my-6 grid gap-4 max-w-sm">
            <label>Current password<input name="current" type="password" required autoComplete="current-password" className="block w-full rounded bg-slate-800 p-2" /></label>
            <label>New password (at least 12 characters)<input name="next" type="password" minLength={12} maxLength={1024} required autoComplete="new-password" className="block w-full rounded bg-slate-800 p-2" /></label>
            <button disabled={loading}>Change password and sign out everywhere</button>
          </form>}
          <h1 className="mt-6 text-3xl font-semibold">Catalog availability</h1>
          <p className="mt-3 text-slate-300">See which approved warehouse tables are ready for analysis.</p>
          <form onSubmit={load} className="my-8"><button disabled={loading} className="rounded bg-cyan-400 px-4 py-2 font-medium text-slate-950 disabled:opacity-50">{loading ? "Loading…" : "Load / refresh"}</button></form>
        </>}
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
