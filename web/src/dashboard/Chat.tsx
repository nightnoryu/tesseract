import {useRef, useEffect, useState} from "react";
import {api} from "../api/client";
import type {Dashboard} from "./model";

type Entry = {role: "user" | "assistant"; content: string};
export default function Chat({onCreated, onSessionExpired}: {onCreated: (d: Dashboard) => void; onSessionExpired?: () => void}) {
  const [session, setSession] = useState<string>();
  const [history, setHistory] = useState<Entry[]>([]);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  async function send() {
    if (busy || !message.trim()) return;
    setBusy(true); setError("");
    const request = new AbortController(); controller.current = request;
    try {
      const result = await api.POST("/api/v1/chat", {body: {message, session_id: session}, signal: request.signal});
      if (request.signal.aborted) return;
      if (result.response.status === 401) onSessionExpired?.();
      if (result.data) {
        setSession(result.data.session_id); setHistory(result.data.history); setMessage("");
        if (result.data.dashboard) {setDone(true); onCreated(result.data.dashboard);}
      } else setError(result.error?.error.message ?? "Could not complete chat.");
    } catch {if (!request.signal.aborted) setError("Connection lost. Check the dashboard list before retrying; a draft may have been saved.");}
    finally {if (!request.signal.aborted) setBusy(false);}
  }
  return <section aria-label="Create with chat" className="my-5 rounded-xl border border-slate-700 p-4">
    <h2 className="text-xl mb-2">Create with chat</h2>
    <p className="text-sm text-slate-400 mb-3">Describe the charts you need. Chat uses your approved catalog. History expires after one hour of inactivity.</p>
    <div aria-live="polite" className="space-y-3">{history.map((entry, i) => <p key={i} className="whitespace-pre-wrap"><strong>{entry.role === "user" ? "You" : "Assistant"}: </strong>{entry.content}</p>)}</div>
    {error && <p role="alert" className="text-amber-300 my-3">{error}</p>}
    {busy && <p role="status">Checking the catalog and building your draft…</p>}
    <form className="mt-3" onSubmit={event => {event.preventDefault(); void send();}}>
      <label className="block">Dashboard request<textarea disabled={busy || done} maxLength={4000} value={message} onChange={event => setMessage(event.target.value)} className="block w-full bg-slate-900 border border-slate-600 rounded p-3 my-2" /></label>
      <div className="flex gap-3"><button disabled={busy || done || !message.trim()} type="submit">Send</button><button type="button" disabled={busy} onClick={() => {setSession(undefined); setHistory([]); setDone(false); setError(""); setMessage("");}}>New conversation</button></div>
    </form>
  </section>;
}
