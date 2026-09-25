import { resolve } from "node:path";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [{
    name: "extensionless-auth-pages",
    configureServer(server) {
      const pages = new Set(["/login", "/logout", "/passkeys", "/security"]);
      server.middlewares.use((request, _response, next) => {
        if (request.url) {
          const url = new URL(request.url, "http://localhost");
          if (pages.has(url.pathname)) request.url = `${url.pathname}.html${url.search}`;
        }
        next();
      });
    },
  }],
  build: {
    rollupOptions: {
      input: {
        index: resolve(__dirname, "index.html"),
        login: resolve(__dirname, "login.html"),
        logout: resolve(__dirname, "logout.html"),
        passkeys: resolve(__dirname, "passkeys.html"),
        security: resolve(__dirname, "security.html"),
      },
    },
  },
});
