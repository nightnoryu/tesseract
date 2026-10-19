import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { api } from "../api/client";
import WidgetView from "./WidgetView";
import Dashboards from "./Dashboards";
import { widgetQuery, type Dashboard, type Spec, type Widget } from "./model";
vi.mock("../api/client", () => ({api: {GET: vi.fn(), POST: vi.fn(), PUT: vi.fn()}}));
const widget: Widget = {id: "count", title: "Leads", kind: "kpi", layout: {x: 0, y: 0, width: 6, height: 2}, query: {dataset: "leads", metrics: ["lead_count"], limit: 100}, filter_mappings: [{filter: "region", dimension: "region_id"}]};
const filters: Spec["filters"] = [{id: "region", operator: "eq", values: [{type: "integer", value: "9007199254740993"}]}];
const dashboard: Dashboard = {id: "abc", owner: "owner", revision: 3, published_revision: 0, created_at: "", updated_at: "", spec: {version: 1, title: "Sales", widgets: [widget], filters}, widget_states: [{id: "count", status: "ready"}]};
const success = (value: unknown) => ({data: value, response: {status: 200}});
afterEach(() => {cleanup(); window.history.replaceState(null, "", "/"); vi.resetAllMocks();});
describe("live dashboard", () => {
  it("maps only explicit filters and preserves typed precision and base filters", () => {
    const original = {...widget, query: {...widget.query, filters: [{dimension: "stage", operator: "eq" as const, values: [{type: "string" as const, value: "won"}]}]}};
    expect(widgetQuery(original, filters).filters).toEqual([...original.query.filters, {dimension: "region_id", operator: "eq", values: filters[0].values}]);
    expect(original.query.filters).toHaveLength(1);
    expect(widgetQuery({...widget, filter_mappings: []}, filters).filters).toEqual([]);
  });
  it.each(["denied", "unavailable", "invalid"])("does not execute a %s widget", status => {
    render(<WidgetView widget={widget} filters={filters} status={status} />);
    expect(screen.getByRole("alert").textContent).toBeTruthy();
    expect(api.POST).not.toHaveBeenCalled();
  });
  it("aborts obsolete requests and ignores late results", async () => {
    let resolve!: (value: unknown) => void;
    vi.mocked(api.POST).mockImplementationOnce(() => new Promise(r => {resolve = r;}) as never).mockResolvedValueOnce(success({columns: ["lead_count"], rows: [[42]]}) as never);
    const view = render(<WidgetView widget={widget} filters={filters} status="ready" />);
    const signal = (vi.mocked(api.POST).mock.calls[0][1] as {signal?: AbortSignal} | undefined)?.signal;
    view.rerender(<WidgetView widget={widget} filters={[...filters]} status="ready" />);
    expect(signal?.aborted).toBe(true);
    await screen.findByText("42");
    await act(async () => resolve(success({columns: ["lead_count"], rows: [[999]]})));
    expect(screen.queryByText("999")).toBeNull();
  });
  it("distinguishes empty results, NULL and zero", async () => {
    vi.mocked(api.POST).mockResolvedValueOnce(success({columns: ["lead_count"], rows: []}) as never);
    const view = render(<WidgetView widget={widget} filters={filters} status="ready" />);
    await screen.findByText("No data matches these filters.");
    vi.mocked(api.POST).mockResolvedValueOnce(success({columns: ["lead_count"], rows: [[null]]}) as never);
    view.rerender(<WidgetView widget={widget} filters={[...filters]} status="ready" />);
    await screen.findByText("—");
    vi.mocked(api.POST).mockResolvedValueOnce(success({columns: ["lead_count"], rows: [[0]]}) as never);
    view.rerender(<WidgetView widget={widget} filters={filters} status="ready" />);
    await screen.findByText("0");
  });
  it.each([[403, "access_denied", "Denied:"], [503, "data_unavailable", "Source unavailable:"], [504, "query_timeout", "Query timed out."]])("shows query failure %s", async (status, code, message) => {
    vi.mocked(api.POST).mockResolvedValueOnce({error: {error: {code}}, response: {status}} as never);
    render(<WidgetView widget={widget} filters={filters} status="ready" />);
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain(message));
  });
  it("opens saved specs, applies filters and preserves edits on revision conflict", async () => {
    vi.mocked(api.GET).mockResolvedValueOnce(success([dashboard]) as never).mockResolvedValueOnce(success(dashboard) as never).mockResolvedValue(success([]) as never);
    vi.mocked(api.POST).mockResolvedValue(success({columns: ["lead_count"], rows: [[12]]}) as never);
    vi.mocked(api.PUT).mockResolvedValue({error: {error: {code: "revision_conflict"}}, response: {status: 409}} as never);
    render(<Dashboards />);
    fireEvent.click(await screen.findByRole("button", {name: /Sales/}));
    await screen.findByText("12");
    fireEvent.change(screen.getByLabelText("region 1"), {target: {value: "7"}});
    fireEvent.click(screen.getByRole("button", {name: "Apply filters"}));
    await waitFor(() => expect((vi.mocked(api.POST).mock.lastCall?.[1] as {body?: unknown} | undefined)?.body).toMatchObject([{id: "region", values: [{type: "integer", value: "7"}]}]));
    fireEvent.click(screen.getByRole("button", {name: "Edit"}));
    const editor = screen.getByLabelText("Dashboard specification") as HTMLTextAreaElement;
    const edited = editor.value.replace('"Sales"', '"New sales"');
    fireEvent.change(editor, {target: {value: edited}});
    fireEvent.click(screen.getByRole("button", {name: "Save draft"}));
    await screen.findByRole("alert");
    expect(editor.value).toBe(edited);
    expect(vi.mocked(api.PUT).mock.calls[0][1]?.body).toMatchObject({expected_revision: 3, spec: {title: "New sales"}});
  });
});
it("returns to sign-in when a widget session expires", async () => {
  const expired = vi.fn();
  vi.mocked(api.POST).mockResolvedValueOnce({response: {status: 401}, error: {error: {code: "unauthorized"}}} as never);
  render(<WidgetView widget={widget} filters={filters} status="ready" onSessionExpired={expired} />);
  await waitFor(() => expect(expired).toHaveBeenCalledOnce());
});

it("keeps a pending chat request on reload and uses current revisions for publish and discard", async () => {
 let current = {...dashboard, spec: {...dashboard.spec, widgets: [], filters: []}, widget_states: []};
 vi.mocked(api.GET).mockImplementation(async path => success(path === "/api/v1/dashboards" ? [current] : path === "/api/v1/dashboards/{dashboardId}" ? current : []) as never);
 vi.mocked(api.POST).mockImplementation(async path => {
   current = {...current, revision: current.revision + 1, published_revision: path === "/api/v1/dashboards/{dashboardId}/publish" ? current.revision + 1 : current.published_revision};
   return success(current) as never;
 });
 render(<Dashboards />);
 fireEvent.click(await screen.findByRole("button", {name: /Sales/}));
 await screen.findByText("Refine with chat");
 fireEvent.change(screen.getByLabelText("Dashboard request"), {target:{value:"Keep this request"}});
 await waitFor(() => expect((screen.getByRole("button", {name:"Reload data"}) as HTMLButtonElement).disabled).toBe(false));
 current = {...current, revision:4};
 fireEvent.click(screen.getByRole("button", {name:"Reload data"}));
 await screen.findByText(/Draft revision 4/);
 expect((screen.getByLabelText("Dashboard request") as HTMLTextAreaElement).value).toBe("Keep this request");
 fireEvent.click(screen.getByRole("button", {name:"Discard changes"}));
 await screen.findByText(/Draft revision 5/);
 expect(vi.mocked(api.POST).mock.calls[0][0]).toBe("/api/v1/dashboards/{dashboardId}/discard");
 expect((vi.mocked(api.POST).mock.calls[0][1] as {body?: unknown} | undefined)?.body).toEqual({expected_revision:4});
 fireEvent.click(screen.getByRole("button", {name:"Publish"}));
 await screen.findByText(/Published revision 6/);
 expect((vi.mocked(api.POST).mock.calls[1][1] as {body?: unknown} | undefined)?.body).toEqual({expected_revision:5});
});

it("pins saved-widget requests to the displayed revision and asks to reload stale data", async () => {
  vi.mocked(api.POST).mockResolvedValueOnce({response: {status: 409}, error: {error: {code: "revision_conflict"}}} as never);
  render(<WidgetView dashboardId="abc" dashboardRevision={3} widget={widget} filters={filters} status="ready" />);
  await screen.findByText("This dashboard changed. Use Reload data before querying again.");
  expect(api.POST).toHaveBeenCalledWith("/api/v1/dashboards/{dashboardId}/widgets/{widgetId}/query", expect.objectContaining({params: {path: {dashboardId: "abc", widgetId: "count"}, query: {expectedRevision: 3}}, body: filters}));
});
