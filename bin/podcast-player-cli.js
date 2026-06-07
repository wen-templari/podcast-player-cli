#!/usr/bin/env node

const { spawn } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

function resolvePackageName(platform, arch) {
  const targets = new Map([
    ["darwin:x64", "podcast-player-cli-darwin-amd64"],
    ["darwin:arm64", "podcast-player-cli-darwin-arm64"],
    ["linux:x64", "podcast-player-cli-linux-amd64"],
    ["linux:arm64", "podcast-player-cli-linux-arm64"]
  ]);

  return targets.get(`${platform}:${arch}`);
}

const packageName = resolvePackageName(process.platform, process.arch);

if (!packageName) {
  console.error(`podcast-player-cli does not support ${process.platform}/${process.arch} yet.`);
  process.exit(1);
}

let packageRoot;

try {
  packageRoot = path.dirname(require.resolve(`${packageName}/package.json`));
} catch (error) {
  console.error(`podcast-player-cli could not find the installed binary package ${packageName}.`);
  console.error("Reinstall the package and try again.");
  process.exit(1);
}

const binaryPath = path.join(packageRoot, "podcast-player-cli");

if (!fs.existsSync(binaryPath)) {
  console.error(`podcast-player-cli binary is missing from ${packageName}. Reinstall the package and try again.`);
  process.exit(1);
}

const child = spawn(binaryPath, process.argv.slice(2), { stdio: "inherit" });

child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 0);
});

child.on("error", (error) => {
  console.error(`Failed to launch podcast-player-cli: ${error.message}`);
  process.exit(1);
});
