import type { components } from "../api/schema";
export type Dashboard = components["schemas"]["Dashboard"];
export type Spec = components["schemas"]["DashboardSpec"];
export type Widget = components["schemas"]["DashboardWidget"];
export type Result = components["schemas"]["QueryResult"];
export function widgetQuery(widget: Widget, filters: Spec["filters"]) {
  return {...widget.query, filters: [...(widget.query.filters ?? []), ...widget.filter_mappings.map(mapping => {
    const filter = filters.find(f => f.id === mapping.filter);
    if (!filter) throw new Error("Missing dashboard filter mapping.");
    return {dimension: mapping.dimension, operator: filter.operator, values: filter.values};
  })]};
}
export function failure(status: number, code?: string): string {
  if (status === 401) return "Your session expired. Sign in again.";
  if (code === "dashboard_access_denied") return "You do not have access to this dashboard. Ask its owner for a viewer grant.";
  if (status === 403) return "Denied: you do not have permission to query this data.";
  if (status === 409) return "This dashboard changed. Reload before saving; copy your edits first.";
  if (code === "data_unavailable") return "Source unavailable: catalog metadata is missing, stale, or incompatible, or the warehouse cannot be reached.";
  if (status === 400) return "Invalid specification or query. Check semantic IDs, filters, and layout against the approved catalog.";
  if (status === 404) return "Dashboard not found or access is no longer available.";
  if (status === 504) return "Query timed out. Narrow your filters and retry.";
  return "The request failed. Try again.";
}
export function cell(value: unknown): string {
  return value == null ? "—" : typeof value === "object" ? "Unsupported value" : String(value);
}
