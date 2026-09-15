#!/usr/bin/env node

import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dependencyRoot = process.env.FLOW_MERMAID_NODE_MODULES;
if (!dependencyRoot) {
  fail("FLOW_MERMAID_NODE_MODULES must point at the pinned Mermaid installation");
}

const require = createRequire(join(dependencyRoot, "package.json"));
const { JSDOM } = require("jsdom");

const generated = spawnSync("go", ["run", "./scripts/flow-mermaid-fixture.go"], {
  cwd: repo,
  encoding: "utf8",
});
if (generated.status !== 0) {
  fail(`Go fixture failed (${generated.status}):\n${generated.stderr}`);
}

const markdown = generated.stdout;
const match = markdown.match(/```mermaid\n([\s\S]*?)\n```/);
if (!match) {
  fail("Go renderer did not emit exactly one Mermaid fenced block");
}
const diagram = match[1];

for (const required of [
  "flowchart TD",
  "n0 -->|\"resolved #124; #34;quoted#34;\"| n1",
  "n1 -.->|\"callback #47; inferred\"| n2",
  "n2 --x n3",
  "class n1 changed",
  "#47;",
  "#60;",
  "#124;",
  "#96;",
]) {
  if (!diagram.includes(required)) {
    fail(`Mermaid fixture is missing ${JSON.stringify(required)}:\n${diagram}`);
  }
}
if (markdown.includes("reflection target <unknown>")) {
  fail("unresolved evidence leaked raw HTML into the rendered Markdown");
}
if (!markdown.includes("Unresolved connections and coverage limits:")) {
  fail("unresolved coverage evidence was omitted from the rendered Markdown");
}

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
for (const key of [
  "window",
  "document",
  "HTMLElement",
  "SVGElement",
  "Node",
  "Element",
  "DOMParser",
  "XMLSerializer",
  "MutationObserver",
]) {
  if (dom.window[key]) {
    globalThis[key] = dom.window[key];
  }
}
Object.defineProperty(globalThis, "navigator", {
  configurable: true,
  value: dom.window.navigator,
});
globalThis.getComputedStyle = dom.window.getComputedStyle.bind(dom.window);
const createDOMPurify = require("dompurify");
globalThis.DOMPurify = createDOMPurify(dom.window);
const mermaid = require("mermaid").default;

mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });
try {
  await mermaid.parse(diagram);
} catch (error) {
  fail(`Mermaid ${require("mermaid/package.json").version} rejected emitted syntax: ${error}`);
}

process.stdout.write(
  `Mermaid smoke passed (${require("mermaid/package.json").version}); ` +
    `${diagram.split("\n").length} diagram lines, ${Buffer.byteLength(diagram)} bytes\n`,
);

function fail(message) {
  console.error(`flow-mermaid-smoke: ${message}`);
  process.exit(1);
}
