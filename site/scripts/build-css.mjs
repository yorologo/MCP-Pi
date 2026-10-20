import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const siteDir = path.resolve(__dirname, "..");
const configFile = path.join(siteDir, "tailwind.config.js");
const inputFile = path.join(siteDir, "assets", "css", "tailwind-input.css");
const outputFile = path.join(siteDir, "assets", "css", "site.css");

// Candidate paths for tailwindcss CLI
const candidates = [
  path.join(siteDir, "node_modules", "tailwindcss", "lib", "cli.js"),
  path.join(siteDir, "..", "tailwind", "node_modules", "tailwindcss", "lib", "cli.js"),
  // Check npm cache in home
  path.join(process.env.HOME || "", ".npm", "_npx", "a040215083095e7d", "node_modules", "tailwindcss", "lib", "cli.js"),
];

let cliPath = candidates.find((p) => fs.existsSync(p));

let res;
if (cliPath) {
  res = spawnSync(process.execPath, [
    cliPath,
    "-c", configFile,
    "-i", inputFile,
    "-o", outputFile,
    "--minify",
  ], { stdio: "inherit" });
} else {
  // Fall back to npx
  res = spawnSync("npx", [
    "--yes", "tailwindcss@3.4.17",
    "-c", configFile,
    "-i", inputFile,
    "-o", outputFile,
    "--minify",
  ], { stdio: "inherit", shell: true });
}

if (res.status !== 0) {
  console.error("Tailwind CSS build failed");
  process.exit(res.status ?? 1);
}

console.log("Tailwind CSS built successfully -> " + outputFile);
