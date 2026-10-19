import {useRef, useEffect, useState} from "react";
import {api} from "../api/client";
import type {components} from "../api/schema";
import type {Dashboard} from "./model";

type Entry = {role: "user" | "assistant"; content: string};
type Turn = components["schemas"]["DashboardChatTurn"];
export default function Chat({dashboard, onCreated, onSessionExpired}: {dashboard?: Dashboard; onCreated: (d: Dashboard) => void; onSessionExpired?: () => void}) {
  const [session, setSession] = useState<string>();
  const [history, setHistory] = useState<Entry[]>([]);
  const [turns, setTurns] = useState<Turn[]>([]);
  const [offset, setOffset] = useState(0);
  const [more, setMore] = useState(false);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => {
    if (!dashboard) return;
    const request = new AbortController();
    setLoading(true);
    api.GET("/api/v1/dashboards/{dashboardId}/chat", {params: {path: {dashboardId: dashboard.id}, query: {limit: 10, offset}}, signal: request.signal}).then(result => {
      if (request.signal.aborted) return;
      if (result.response.status === 401) onSessionExpired?.();
      if (result.data) {setTurns([...result.data].reverse()); setMore(result.data.length === 10);}
      else setError(result.error?.error.message ?? "Could not load chat history.");
    }).catch(() => {if (!request.signal.aborted) setError("Could not load chat history.");})
      .finally(() => {if (!request.signal.aborted) setLoading(false);});
    return () => request.abort();
  }, [dashboard?.id, dashboard?.revision, offset, refresh, onSessionExpired]);
  async function send() {
    if (busy || !message.trim()) return;
    setBusy(true); setError("");
    const request = new AbortController(); controller.current = request;
    try {
      const body = dashboard ? {message, dashboard_id: dashboard.id, expected_revision: dashboard.revision} : {message, session_id: session};
      const result = await api.POST("/api/v1/chat", {body, signal: request.signal});
      if (request.signal.aborted) return;
      if (result.response.status === 401) onSessionExpired?.();
      if (result.data) {
        setSession(result.data.session_id); setHistory(result.data.history); setMessage(""); setOffset(0); setRefresh(r => r + 1);
        if (result.data.dashboard) {setDone(!dashboard); onCreated(result.data.dashboard);}
      } else setError(result.response.status === 409 ? "This draft changed. Reload data before retrying. Your request has been kept." : (result.error?.error.message ?? "Could not complete chat."));
    } catch {if (!request.signal.aborted) setError("Connection lost. Reload the dashboard before retrying; changes may have been saved.");}
    finally {if (!request.signal.aborted) setBusy(false);}
  }
  const heading = dashboard ? "Refine with chat" : "Create with chat";
  return <section aria-label={heading} className="my-5 rounded-xl border border-slate-700 p-4">
    <h2 className="text-xl mb-2">{heading}</h2>
    <p className="text-sm text-slate-400 mb-3">{dashboard ? "Each accepted edit is saved as a draft revision. Publish when ready, or discard changes to restore the last published version (the initial draft if unpublished)." : "Describe the charts you need. Chat uses your approved catalog. History is saved with the created draft; unfinished conversations expire after one hour."}</p>
    <div aria-live="polite" className="space-y-3">{dashboard ? turns.map((turn, i) => <div key={i}>
      <p className="text-xs text-slate-400">Revision {turn.revision} · {turn.kind}</p>
      {turn.entries.map((entry, j) => <p key={j} className="whitespace-pre-wrap"><strong>{entry.role === "user" ? "You" : "Assistant"}: </strong>{entry.content}</p>)}
      {turn.operations.length > 0 && <details><summary>Saved operations ({turn.operations.length})</summary><pre className="text-xs overflow-auto">{JSON.stringify(turn.operations, null, 2)}</pre></details>}
    </div>) : history.map((entry, i) => <p key={i} className="whitespace-pre-wrap"><strong>{entry.role === "user" ? "You" : "Assistant"}: </strong>{entry.content}</p>)}</div>
    {dashboard && <div className="flex gap-3 my-2"><button disabled={busy || loading || !more} onClick={() => setOffset(o => o + 10)}>Older messages</button><button disabled={busy || loading || offset === 0} onClick={() => setOffset(o => Math.max(0, o - 10))}>Newer messages</button></div>}
    {error && <p role="alert" className="text-amber-300 my-3">{error}</p>}
    {loading && <p role="status">Loading saved chat…</p>}
    {busy && <p role="status">Checking the catalog and {dashboard ? "applying your edits" : "building your draft"}…</p>}
    <form className="mt-3" onSubmit={event => {event.preventDefault(); void send();}}>
      <label className="block">Dashboard request<textarea disabled={busy || done} maxLength={4000} value={message} onChange={event => setMessage(event.target.value)} className="block w-full bg-slate-900 border border-slate-600 rounded p-3 my-2" /></label>
      <div className="flex gap-3"><button disabled={busy || done || !message.trim()} type="submit">Send</button>{!dashboard && <button type="button" disabled={busy} onClick={() => {setSession(undefined); setHistory([]); setDone(false); setError(""); setMessage("");}}>New conversation</button>}</div>
    </form>
  </section>;
}
