import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const siteDir = fileURLToPath(new URL("..", import.meta.url));
const result = spawnSync("npx", ["--yes", "tailwindcss@3.4.17", "-c", "tailwind.config.js",
  "-i", "assets/css/tailwind-input.css", "-o", "assets/css/site.css", "--minify"],
{ cwd: siteDir, stdio: "inherit" });
if (result.error) console.error(result.error.message);
process.exit(result.status ?? 1);
