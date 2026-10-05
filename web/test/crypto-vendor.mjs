// Keep the embedded, MIT-licensed HTTP encryption fallback identical to the
// pinned npm distribution. Runtime deployments need neither npm nor a CDN.
import assert from "node:assert/strict";
import { readFile, mkdir, writeFile } from "node:fs/promises";
const packageRoot = new URL("../node_modules/asmcrypto.js/", import.meta.url);
const targetRoot = new URL("../assets/vendor/", import.meta.url);
const { version } = JSON.parse(await readFile(new URL("package.json", packageRoot), "utf8"));
const license = await readFile(new URL("LICENSE", packageRoot), "utf8");
const source = await readFile(new URL("asmcrypto.all.es8.min.js", packageRoot), "utf8");
const expected = `/* asmcrypto.js ${version}\n${license}\n*/\n${source}`;
const output = new URL("asmcrypto.js", targetRoot);
if (process.argv.includes("--write")) {
  await mkdir(targetRoot, { recursive: true });
  await writeFile(output, expected);
} else {
  assert.equal(await readFile(output, "utf8"), expected, "run npm run vendor:crypto after updating asmcrypto.js");
}
