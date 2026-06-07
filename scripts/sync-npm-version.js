#!/usr/bin/env node

const fs = require("node:fs");
const path = require("node:path");

const version = process.argv[2];

if (!version) {
  console.error("Usage: node scripts/sync-npm-version.js <version>");
  process.exit(1);
}

const rootPackagePath = path.join(__dirname, "..", "package.json");
const platformPackageDirs = [
  "npm/darwin-amd64",
  "npm/darwin-arm64",
  "npm/linux-amd64",
  "npm/linux-arm64"
];

function updateJson(filePath, update) {
  const data = JSON.parse(fs.readFileSync(filePath, "utf8"));
  update(data);
  fs.writeFileSync(filePath, `${JSON.stringify(data, null, 2)}\n`);
}

updateJson(rootPackagePath, (pkg) => {
  pkg.version = version;
  for (const name of Object.keys(pkg.optionalDependencies)) {
    pkg.optionalDependencies[name] = version;
  }
});

for (const dir of platformPackageDirs) {
  updateJson(path.join(__dirname, "..", dir, "package.json"), (pkg) => {
    pkg.version = version;
  });
}
