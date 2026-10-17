import { useEffect, useState } from "react";
import { api } from "./api/client";

export default function App() {
  const [status, setStatus] = useState("Connecting to service…");
  useEffect(() => {
    const controller = new AbortController();
    api.GET("/healthz", { signal: controller.signal })
      .then(({ data }) => {
        if (!controller.signal.aborted) setStatus(data?.status === "ok" ? "Service is online." : "Service is unavailable.");
      })
      .catch(() => {
        if (!controller.signal.aborted) setStatus("Service is unavailable.");
      });
    return () => controller.abort();
  }, []);

  return (
    <main className="min-h-screen bg-slate-950 px-6 py-24 text-slate-100">
      <div className="mx-auto max-w-2xl">
        <p className="text-sm font-semibold uppercase tracking-widest text-cyan-400">Tesseract</p>
        <h1 className="mt-6 text-4xl font-semibold tracking-tight">A clearer view of your business.</h1>
        <p className="mt-6 text-lg leading-8 text-slate-300">
          Create and refine marketing and sales dashboards through conversation.
        </p>
        <p role="status" className="mt-6 text-sm text-slate-400">{status}</p>
        <p className="mt-10 rounded-xl border border-slate-700 p-5 text-sm text-slate-400">
          Development preview. Dashboard creation and sign-in are coming in later milestones.
        </p>
      </div>
    </main>
  );
}
