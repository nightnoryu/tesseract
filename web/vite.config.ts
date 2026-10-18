import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [{
            name: "chart-renderer",
            test: /[\\/]node_modules[\\/]zrender[\\/]/,
          }],
        },
      },
    },
  },
  server: { host: "127.0.0.1", port: 5173, strictPort: true, proxy: { "/api": "http://127.0.0.1:8080", "/healthz": "http://127.0.0.1:8080", "/readyz": "http://127.0.0.1:8080" } },
});
