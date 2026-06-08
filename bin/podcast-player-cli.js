#!/usr/bin/env node

const { spawn } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const binaryPath = path.join(__dirname, "native", process.platform === "win32" ? "podcast-player-cli.exe" : "podcast-player-cli");

if (!fs.existsSync(binaryPath)) {
  console.error("podcast-player-cli binary is missing. Reinstall the package to download the executable for this platform.");
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
