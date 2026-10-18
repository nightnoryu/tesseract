import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/react";
import Chart from "./Chart";
import type { Widget } from "./model";
const mocks = vi.hoisted(() => ({setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn(), disconnect: vi.fn(), observe: vi.fn()}));
vi.mock("echarts/core", () => ({use: vi.fn(), init: () => mocks}));
afterEach(() => {cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals();});
it.each(["line", "bar"] as const)("renders %s with fixed safe options, preserves gaps, resizes and disposes", kind => {
  let resized!: () => void;
  vi.stubGlobal("ResizeObserver", class { constructor(callback: () => void) {resized = callback;} observe = mocks.observe; disconnect = mocks.disconnect; });
  const widget: Widget = {id: "chart", title: "Trend", kind, layout: {x: 0, y: 0, width: 12, height: 4}, query: {dataset: "leads", metrics: ["lead_count"], dimensions: [{id: "created", grain: "month"}], limit: 10}, filter_mappings: []};
  const view = render(<Chart widget={widget} result={{columns: ["created", "lead_count"], rows: [["<img src=x onerror=alert(1)>", "12.5"], ["2026-02-01", null], ["2026-03-01", "0"]]}} />);
  const option = mocks.setOption.mock.lastCall![0];
  expect(option.tooltip).toEqual({trigger: "axis", renderMode: "richText", confine: true});
  expect(option.series[0]).toMatchObject({type: kind, data: [12.5, null, 0], connectNulls: false});
  expect(view.container.querySelector("img")).toBeNull();
  resized(); expect(mocks.resize).toHaveBeenCalled();
  view.unmount(); expect(mocks.dispose).toHaveBeenCalled(); expect(mocks.disconnect).toHaveBeenCalled();
});
