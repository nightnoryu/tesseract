import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { api } from "../api/client";
import App from "../App";
import Share from "./Share";
import type { Dashboard } from "./model";
vi.mock("../api/client", () => ({api: {GET: vi.fn(), POST: vi.fn(), DELETE: vi.fn()}}));
const id = "00000000-0000-0000-0000-000000000001";
const viewer = "00000000-0000-0000-0000-000000000002";
const dashboard: Dashboard = {id, owner: "owner", role: "viewer", revision: 2, published_revision: 2, created_at: "", updated_at: "", spec: {version: 1, title: "Shared leads", widgets: [], filters: []}, widget_states: []};
const ok = (data?: unknown) => ({data, response: {status: 200, ok: true}});
afterEach(() => {cleanup(); window.history.replaceState(null, "", "/"); sessionStorage.clear(); vi.resetAllMocks();});
it("grants a named local account, lists access, revokes it and copies a stable link", async () => {
  let granted = false;
  vi.mocked(api.GET).mockImplementation(async () => ok(granted ? [{user_id: viewer, email: "viewer@example.com"}] : []) as never);
  vi.mocked(api.POST).mockImplementation(async () => {granted = true; return ok() as never;});
  vi.mocked(api.DELETE).mockImplementation(async () => {granted = false; return ok() as never;});
  const copy = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {value: {writeText: copy}, configurable: true});
  render(<Share id={id} published />);
  fireEvent.change(screen.getByLabelText("Colleague’s account email"), {target: {value: "viewer@example.com"}});
  fireEvent.click(screen.getByRole("button", {name: "Grant viewer access"}));
  await screen.findByText("viewer@example.com · Viewer");
  expect(api.POST).toHaveBeenCalledWith("/api/v1/dashboards/{dashboardId}/shares", {params: {path: {dashboardId: id}}, body: {email: "viewer@example.com"}});
  fireEvent.click(screen.getByRole("button", {name: "Copy link"}));
  await waitFor(() => expect(copy).toHaveBeenCalledWith(`${window.location.origin}/?dashboard=${id}`));
  fireEvent.click(screen.getByRole("button", {name: "Revoke"}));
  await waitFor(() => expect(screen.queryByText("viewer@example.com · Viewer")).toBeNull());
  expect(api.DELETE).toHaveBeenCalledWith("/api/v1/dashboards/{dashboardId}/shares/{userId}", {params: {path: {dashboardId: id, userId: viewer}}});
});
it("opens the shared URL after password login and hides owner controls and chat", async () => {
  window.history.replaceState(null, "", `/?dashboard=${id}`);
  vi.mocked(api.GET).mockImplementation(async path => {
    if (path === "/api/v1/auth/options") return ok({oidc: false}) as never;
    if (path === "/api/v1/auth/session") return {response: {status: 401}} as never;
    return ok(path === "/api/v1/dashboards" ? [dashboard] : dashboard) as never;
  });
  vi.mocked(api.POST).mockResolvedValue(ok({email: "viewer@example.com"}) as never);
  render(<App />);
  fireEvent.change(await screen.findByLabelText("Email"), {target: {value: "viewer@example.com"}});
  fireEvent.change(screen.getByLabelText("Password"), {target: {value: "example-password"}});
  fireEvent.click(screen.getByRole("button", {name: "Sign in"}));
  await screen.findByRole("heading", {name: "Shared leads"});
  for (const name of ["Edit", "Publish", "Discard changes", "Share"]) expect(screen.queryByRole("button", {name})).toBeNull();
  expect(screen.queryByLabelText("Dashboard request")).toBeNull();
});
it("restores the shared destination after the OIDC callback", async () => {
  sessionStorage.setItem("dashboard_return", id);
  vi.mocked(api.GET).mockImplementation(async path => ok(path === "/api/v1/auth/options" ? {oidc: true} : path === "/api/v1/auth/session" ? {email: "viewer@example.com"} : path === "/api/v1/dashboards" ? [dashboard] : dashboard) as never);
  render(<App />);
  await screen.findByRole("heading", {name: "Shared leads"});
  expect(window.location.search).toBe(`?dashboard=${id}`);
  expect(sessionStorage.getItem("dashboard_return")).toBeNull();
});
it("shows a denial without dashboard content when a signed-in recipient has no grant", async () => {
  window.history.replaceState(null, "", `/?dashboard=${id}`);
  vi.mocked(api.GET).mockImplementation(async path => {
    if (path === "/api/v1/dashboards/{dashboardId}") return {response: {status: 403}, error: {error: {code: "dashboard_access_denied"}}} as never;
    return ok(path === "/api/v1/auth/options" ? {} : path === "/api/v1/auth/session" ? {email: "third@example.com"} : []) as never;
  });
  render(<App />);
  await screen.findByRole("alert");
  expect(screen.queryByRole("heading", {name: "Shared leads"})).toBeNull();
});
