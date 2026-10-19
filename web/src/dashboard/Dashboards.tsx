import { useEffect, useState } from "react";
import { api } from "../api/client";
import Chat from "./Chat";
import WidgetView from "./WidgetView";
import { failure, type Dashboard, type Spec } from "./model";
const blank: Spec = {version: 1, title: "Untitled dashboard", filters: [], widgets: []};
export default function Dashboards({onSessionExpired}: {onSessionExpired?: () => void}) {
  const [items, setItems] = useState<Dashboard[]>([]);
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<Dashboard>();
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState("");
  const [filters, setFilters] = useState<Spec["filters"]>([]);
  const [filterDraft, setFilterDraft] = useState<Spec["filters"]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);
  const [generation, setGeneration] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setBusy(true); setError("");
    api.GET("/api/v1/dashboards", {params: {query: {limit: 10, offset}}, signal: controller.signal}).then(({data, error, response}) => {
      if (controller.signal.aborted) return;
      if (response.status === 401) onSessionExpired?.();
      if (data) setItems(data); else {setItems([]); setError(failure(response.status, error?.error.code));}
    }).catch(() => {if (!controller.signal.aborted) setError("Cannot load dashboards.");}).finally(() => {if (!controller.signal.aborted) setBusy(false);});
    return () => controller.abort();
  }, [offset, reload, onSessionExpired]);
  function accept(dashboard: Dashboard) {
    setSelected(dashboard); setFilters(dashboard.spec.filters); setFilterDraft(dashboard.spec.filters);
    setEditing(false); setGeneration(g => g + 1);
  }
  async function open(id: string) {
    setBusy(true); setError(""); if (selected?.id !== id) setSelected(undefined); setEditing(false);
    try {
      const {data, error, response} = await api.GET("/api/v1/dashboards/{dashboardId}", {params: {path: {dashboardId: id}}});
      if (response.status === 401) onSessionExpired?.();
      if (data) accept(data); else setError(failure(response.status, error?.error.code));
    } catch {setError("Cannot load dashboard.");} finally {setBusy(false);}
  }
  async function save() {
    setError("");
    let spec: Spec;
    try {spec = JSON.parse(text);} catch {setError("Enter valid JSON. Your edits have been kept."); return;}
    setBusy(true);
    try {
      const result = selected
        ? await api.PUT("/api/v1/dashboards/{dashboardId}/draft", {params: {path: {dashboardId: selected.id}}, body: {expected_revision: selected.revision, spec}})
        : await api.POST("/api/v1/dashboards", {body: spec});
      if (result.response.status === 401) onSessionExpired?.();
      if (result.data) {accept(result.data); setReload(r => r + 1);} else setError(failure(result.response.status, result.error?.error.code));
    } catch {setError("Could not save. Your edits have been kept.");} finally {setBusy(false);}
  }
  async function publish() {
    if (!selected) return;
    setBusy(true); setError("");
    try {
      const {data, error, response} = await api.POST("/api/v1/dashboards/{dashboardId}/publish", {params: {path: {dashboardId: selected.id}}, body: {expected_revision: selected.revision}});
      if (response.status === 401) onSessionExpired?.();
      if (data) {accept(data); setReload(r => r + 1);} else setError(failure(response.status, error?.error.code));
    } catch {setError("Could not publish dashboard.");} finally {setBusy(false);}
  }
  async function discard() {
    if (!selected) return;
    setBusy(true); setError("");
    try {
      const {data, error, response} = await api.POST("/api/v1/dashboards/{dashboardId}/discard", {params: {path: {dashboardId: selected.id}}, body: {expected_revision: selected.revision}});
      if (response.status === 401) onSessionExpired?.();
      if (data) {accept(data); setReload(r => r + 1);} else setError(failure(response.status, error?.error.code));
    } catch {setError("Could not discard changes. Reload the dashboard before retrying.");} finally {setBusy(false);}
  }
  return <section className="dashboard-workspace mt-8" aria-label="Dashboards">
    <div className="flex flex-wrap items-center gap-3"><h1 className="text-3xl font-semibold mr-auto">Dashboards</h1><button disabled={busy || editing} onClick={() => {setSelected(undefined); setError("");}}>New chat dashboard</button><button disabled={busy || editing} onClick={() => {setSelected(undefined); setText(JSON.stringify(blank, null, 2)); setEditing(true); setError("");}}>New dashboard</button><button disabled={busy || editing} onClick={() => setReload(r => r + 1)}>Refresh list</button></div>
    {!editing && <Chat key={selected?.id ?? "new"} dashboard={selected} onSessionExpired={onSessionExpired} onCreated={dashboard => {accept(dashboard); setReload(r => r + 1);}} />}
    <div className="mt-6 flex flex-col lg:flex-row gap-6">
      <aside className="lg:w-64 shrink-0" aria-label="Saved dashboards">
        {busy && <p role="status">Loading…</p>}
        {!busy && items.length === 0 && <p>No saved dashboards on this page.</p>}
        <ul className="space-y-2">{items.map(d => <li key={d.id}><button className="w-full text-left" disabled={busy || editing} aria-current={selected?.id === d.id ? "page" : undefined} onClick={() => void open(d.id)}>{d.spec.title}<span className="block text-xs text-slate-400">Revision {d.revision}</span></button></li>)}</ul>
        <div className="mt-4 flex gap-2"><button disabled={busy || editing || offset === 0} onClick={() => setOffset(o => Math.max(0, o - 10))}>Previous</button><button disabled={busy || editing || items.length < 10} onClick={() => setOffset(o => o + 10)}>Next</button></div>
      </aside>
      <div className="min-w-0 flex-1">
        {error && <p role="alert" className="mb-4 text-amber-300">{error}</p>}
        {editing ? <form onSubmit={event => {event.preventDefault(); void save();}}>
          <h2 className="text-xl mb-3">{selected ? "Edit dashboard" : "Create dashboard"}</h2>
          <p className="mb-3 text-slate-300">Edit the version 1 specification using approved semantic IDs. The server validates queries, filters, and the non-overlapping 12-column layout before saving.</p>
          <label className="block">Dashboard specification<textarea className="mt-2 w-full h-96 font-mono text-sm bg-slate-900 border border-slate-600 rounded p-3" spellCheck={false} value={text} onChange={event => setText(event.target.value)} disabled={busy} /></label>
          <div className="mt-3 flex gap-3"><button disabled={busy} type="submit">Save draft</button><button type="button" disabled={busy} onClick={() => {setEditing(false); setError("");}}>Cancel edits</button></div>
        </form> : selected ? <>
          <div className="flex flex-wrap items-center gap-3"><h2 className="text-2xl mr-auto">{selected.spec.title}</h2><button disabled={busy} onClick={() => {setText(JSON.stringify(selected.spec, null, 2)); setEditing(true); setError("");}}>Edit</button><button disabled={busy} onClick={() => void open(selected.id)}>Reload data</button><button disabled={busy || selected.revision === 1 || selected.published_revision === selected.revision} onClick={() => void discard()}>Discard changes</button><button disabled={busy || selected.published_revision === selected.revision} onClick={() => void publish()}>Publish</button></div>
          <p className="my-3 text-sm text-slate-400">Draft revision {selected.revision} · {selected.published_revision ? `Published revision ${selected.published_revision}` : "Not published"} · Live warehouse results</p>
          {filterDraft.length > 0 && <form className="mb-5 rounded border border-slate-700 p-4" onSubmit={event => {event.preventDefault(); setFilters(filterDraft); setGeneration(g => g + 1);}}>
            <h3 className="font-semibold mb-3">Filters</h3><div className="flex flex-wrap gap-4">{filterDraft.map((filter, fi) => <fieldset key={filter.id}><legend>{filter.id} ({filter.operator})</legend>{filter.values?.map((value, vi) => <label key={vi} className="block text-sm">{value.type} {vi + 1}<input aria-label={`${filter.id} ${vi + 1}`} className="block bg-slate-800 rounded p-2 my-1" value={value.value} maxLength={2048} onChange={event => setFilterDraft(current => current.map((f, i) => i !== fi ? f : {...f, values: f.values?.map((v, j) => j !== vi ? v : {...v, value: event.target.value})}))} /></label>)}</fieldset>)}</div>
            <button className="mt-3" type="submit">Apply filters</button><p className="mt-2 text-xs text-slate-400">Applies only to explicitly mapped widgets. Viewer changes are not saved.</p>
          </form>}
          {selected.spec.widgets.length === 0 && <p>This dashboard has no widgets. Edit its specification to add one.</p>}
          <div className="dashboard-grid" key={`${selected.id}:${selected.revision}:${generation}`}>{[...selected.spec.widgets].sort((a, b) => a.layout.y - b.layout.y || a.layout.x - b.layout.x).map(widget => <article className="dashboard-widget rounded-xl border border-slate-700 bg-slate-900 p-4 overflow-auto" key={widget.id} style={{gridColumn: `${widget.layout.x + 1} / span ${widget.layout.width}`, gridRow: `${widget.layout.y + 1} / span ${widget.layout.height}`}}><h3 className="text-lg font-semibold mb-4">{widget.title}</h3><WidgetView onSessionExpired={onSessionExpired} widget={widget} filters={filters} status={selected.widget_states.find(state => state.id === widget.id)?.status ?? "invalid"} /></article>)}</div>
        </> : <p>Select a saved dashboard or create one.</p>}
      </div>
    </div>
  </section>;
}
