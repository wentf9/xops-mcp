import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { english } from "../assets/en.js";
import { resolveLocale, supportedLocale, translate } from "../assets/i18n.js";

assert.equal(resolveLocale(null, ["fr-FR", "en-GB"]), "en");
assert.equal(resolveLocale(null, ["zh-TW", "en"]), "zh-CN");
assert.equal(resolveLocale("en", ["zh-CN"]), "en");
assert.equal(resolveLocale("zh", ["en-US"]), "zh-CN");
assert.equal(resolveLocale("invalid", ["de-DE"]), "en");
assert.equal(supportedLocale("not-zh"), null);
assert.equal(translate("zh-CN", "编辑{0}", { 0: "节点" }), "编辑节点");
assert.equal(translate("en", "编辑{0}", { 0: "Node" }), "Edit Node");
assert.equal(translate("en", "{0}已保存", { 0: "中文 {1} <script>" }), "中文 {1} <script> saved");
for (const [source, target] of Object.entries(english)) {
  const params = text => [...text.matchAll(/\{\w+\}/g)].map(x => x[0]).sort();
  assert.deepEqual(params(target), params(source), "translation parameters: " + source);
  assert(!/\p{Script=Han}/u.test(target), "untranslated English entry: " + source);
}
for (const name of ["app.js", "auth.js", "naming.js"]) {
  const code = await readFile(new URL("../assets/" + name, import.meta.url), "utf8");
  for (const match of code.matchAll(/\b(?:t|tr)\(("(?:[^"\\]|\\.)*")/g)) {
    const key = JSON.parse(match[1]);
    assert(Object.hasOwn(english, key), "missing English translation: " + key);
  }
}
console.log("PASS i18n language selection, interpolation, and catalog coverage");
