import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const publicFiles = [
  "index.html", "demo/index.html", "guide/index.html", "404.html", ".nojekyll",
  "assets/css/site.css", "assets/js/site.js", "assets/js/demo-scenarios.js",
  "assets/brand/isotipo.svg", "assets/brand/favicon.svg", "assets/brand/social.png",
];

export function stageSite(source, destination) {
  if (fs.existsSync(destination)) throw new Error("Artifact destination must not exist");
  for (const file of publicFiles) {
    let current = source;
    for (const part of file.split("/")) {
      current = path.join(current, part);
      if (fs.lstatSync(current).isSymbolicLink()) throw new Error(`Symbolic link is not publishable: ${file}`);
    }
    if (!fs.statSync(current).isFile()) throw new Error(`Not a public file: ${file}`);
  }
  for (const file of publicFiles) {
    const target = path.join(destination, file);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.copyFileSync(path.join(source, file), target);
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  if (!process.argv[2]) throw new Error("Usage: node site/scripts/stage-site.mjs <new-artifact-directory>");
  stageSite(fileURLToPath(new URL("..", import.meta.url)), path.resolve(process.argv[2]));
}
