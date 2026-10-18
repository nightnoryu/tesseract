import { expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import ChartBoundary from "./ChartBoundary";
it("isolates a failed chart while keeping exact data accessible", () => {
  const log = vi.spyOn(console, "error").mockImplementation(() => {});
  function Broken(): never {throw new Error("chunk failed");}
  try {
    render(<><ChartBoundary><Broken /></ChartBoundary><table><tbody><tr><td>123.456789</td></tr></tbody></table></>);
    expect(screen.getByRole("alert").textContent).toContain("chart could not be displayed");
    expect(screen.getByRole("cell").textContent).toBe("123.456789");
  } finally {cleanup(); log.mockRestore();}
});
