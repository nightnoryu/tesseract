import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { components } from "../api/schema";
import { failure } from "./model";

export default function Share({id, published, onSessionExpired}: {id: string; published: boolean; onSessionExpired?: () => void}) {
  const [shares, setShares] = useState<components["schemas"]["DashboardShare"][]>([]);
  const [email, setEmail] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);
  const url = new URL(`/?dashboard=${encodeURIComponent(id)}`, window.location.origin).href;
  useEffect(() => {
    const controller = new AbortController();
    api.GET("/api/v1/dashboards/{dashboardId}/shares", {params: {path: {dashboardId: id}}, signal: controller.signal}).then(({data, error, response}) => {
      if (controller.signal.aborted) return;
      if (response.status === 401) onSessionExpired?.();
      if (data) setShares(data); else setError(failure(response.status, error?.error.code));
    }).catch(() => {if (!controller.signal.aborted) setError("Cannot load access grants.");});
    return () => controller.abort();
  }, [id, reload, onSessionExpired]);
  async function change(userId?: string) {
    setBusy(true); setError(""); setMessage("");
    try {
      const result = userId
        ? await api.DELETE("/api/v1/dashboards/{dashboardId}/shares/{userId}", {params: {path: {dashboardId: id, userId}}})
        : await api.POST("/api/v1/dashboards/{dashboardId}/shares", {params: {path: {dashboardId: id}}, body: {email}});
      if (result.response.status === 401) onSessionExpired?.();
      if (result.response.ok) {setEmail(""); setReload(r => r + 1); setMessage(userId ? "Access revoked." : "Viewer access granted.");}
      else setError(result.error?.error.code === "share_recipient_unavailable" ? "No eligible account matches that email." : failure(result.response.status, result.error?.error.code));
    } catch {setError("Could not update access. Reload the access list before retrying.");} finally {setBusy(false);}
  }
  return <section aria-label="Share dashboard" className="my-4 rounded border border-slate-700 p-4">
    <h3 className="text-lg font-semibold">Share dashboard</h3>
    <p className="my-2">You are the owner. Viewers see the latest published revision with their own data permissions.</p>
    {!published && <p role="status">Publish this dashboard before recipients can open it.</p>}
    <label className="block">Dashboard link<input readOnly value={url} className="block w-full bg-slate-800 p-2 my-2" /></label>
    <button onClick={async () => {try {await navigator.clipboard.writeText(url); setMessage("Link copied.");} catch {setError("Select and copy the dashboard link above.");}}}>Copy link</button>
    <form className="my-4 flex flex-wrap gap-3 items-end" onSubmit={event => {event.preventDefault(); void change();}}>
      <label>Colleague’s account email<input required type="email" maxLength={254} value={email} onChange={event => setEmail(event.target.value)} className="block bg-slate-800 p-2" /></label><button disabled={busy}>Grant viewer access</button>
    </form>
    <ul>{shares.map(share => <li key={share.user_id} className="flex gap-3 items-center my-2"><span>{share.email} · Viewer</span><button disabled={busy} onClick={() => void change(share.user_id)}>Revoke</button></li>)}</ul>
    {message && <p role="status">{message}</p>}{error && <p role="alert" className="text-amber-300">{error}</p>}
  </section>;
}
