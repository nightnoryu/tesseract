import { useEffect, useRef } from "react";
import { init, use } from "echarts/core";
import { BarChart, LineChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import { cell, type Result, type Widget } from "./model";
use([BarChart, LineChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);
export default function Chart({widget, result}: {widget: Widget; result: Result}) {
  const element = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const chart = init(element.current!);
    const dimension = result.columns.indexOf(widget.query.dimensions![0].id);
    chart.setOption({
      animation: false,
      textStyle: {color: "#cbd5e1"},
      tooltip: {trigger: "axis", renderMode: "richText", confine: true},
      legend: {type: "scroll", textStyle: {color: "#cbd5e1"}},
      grid: {left: 65, right: 20, bottom: 65, top: 45},
      xAxis: {type: "category", data: result.rows.map(row => cell(row[dimension]))},
      yAxis: {type: "value"},
      series: widget.query.metrics.map(metric => ({name: metric, type: widget.kind,
        connectNulls: false, data: result.rows.map(row => {
          const value = row[result.columns.indexOf(metric)];
          if (value == null || value === "") return null;
          const number = Number(value);
          return Number.isFinite(number) ? number : null;
        })})),
    });
    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(element.current!);
    return () => { observer.disconnect(); chart.dispose(); };
  }, [widget, result]);
  return <div ref={element} className="h-80 w-full" role="img" aria-label={`${widget.title}, ${widget.kind} chart. Exact values are available in the data table below.`} />;
}
