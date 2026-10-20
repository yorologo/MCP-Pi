import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const siteDir = fileURLToPath(new URL("..", import.meta.url));

const candidates = [
  path.join(siteDir, "node_modules", "tailwindcss", "lib", "cli.js"),
  path.join(siteDir, "..", "tailwind", "node_modules", "tailwindcss", "lib", "cli.js"),
  path.join(process.env.HOME || "", ".npm", "_npx", "a040215083095e7d", "node_modules", "tailwindcss", "lib", "cli.js"),
];

const cliPath = candidates.find((p) => fs.existsSync(p));

let result;
if (cliPath) {
  result = spawnSync(process.execPath, [
    cliPath,
    "-c", "tailwind.config.js",
    "-i", "assets/css/tailwind-input.css",
    "-o", "assets/css/site.css",
    "--minify",
  ], { cwd: siteDir, stdio: "inherit" });
} else {
  result = spawnSync("npx", [
    "--yes", "tailwindcss@3.4.17",
    "-c", "tailwind.config.js",
    "-i", "assets/css/tailwind-input.css",
    "-o", "assets/css/site.css",
    "--minify",
  ], { cwd: siteDir, stdio: "inherit", shell: true });
}

if (result.error) console.error(result.error.message);
process.exit(result.status ?? 1);
