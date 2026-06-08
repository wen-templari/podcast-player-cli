#!/usr/bin/env node

const fs = require("node:fs");
const path = require("node:path");
const https = require("node:https");

const packageJson = require("../package.json");

const OWNER = "wen-templari";
const REPO = "podcast-player-cli";
const VERSION = packageJson.version;
const NATIVE_DIR = path.join(__dirname, "..", "bin", "native");

function resolveTarget(platform, arch) {
  const supportedPlatforms = new Map([
    ["linux", "linux"],
    ["darwin", "darwin"]
  ]);
  const supportedArch = new Map([
    ["x64", "amd64"],
    ["arm64", "arm64"]
  ]);

  const goos = supportedPlatforms.get(platform);
  if (!goos) {
    throw new Error(`Unsupported platform: ${platform}. This package currently supports Linux and macOS.`);
  }

  const goarch = supportedArch.get(arch);
  if (!goarch) {
    throw new Error(`Unsupported architecture: ${arch}. This package currently supports x64 and arm64.`);
  }

  return { goos, goarch };
}

function buildAssetUrl(version, platform, arch) {
  const { goos, goarch } = resolveTarget(platform, arch);
  const asset = `podcast-player-cli_v${version}_${goos}_${goarch}`;
  return `https://github.com/${OWNER}/${REPO}/releases/download/v${version}/${asset}`;
}

function download(url, destination) {
  return new Promise((resolve, reject) => {
    const request = https.get(url, (response) => {
      if (response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
        response.resume();
        download(response.headers.location, destination).then(resolve, reject);
        return;
      }

      if (response.statusCode !== 200) {
        response.resume();
        reject(new Error(`Download failed with status ${response.statusCode} for ${url}`));
        return;
      }

      const file = fs.createWriteStream(destination, { mode: 0o755 });
      response.pipe(file);
      file.on("finish", () => file.close(resolve));
      file.on("error", reject);
    });

    request.on("error", reject);
  });
}

async function install() {
  if (process.env.PODCAST_PLAYER_CLI_SKIP_DOWNLOAD === "1") {
    console.log("Skipping podcast-player-cli binary download.");
    return;
  }

  fs.mkdirSync(NATIVE_DIR, { recursive: true });

  const filename = process.platform === "win32" ? "podcast-player-cli.exe" : "podcast-player-cli";
  const destination = path.join(NATIVE_DIR, filename);
  const url = buildAssetUrl(VERSION, process.platform, process.arch);

  await download(url, destination);
  fs.chmodSync(destination, 0o755);
  console.log(`Installed podcast-player-cli from ${url}`);
}

if (require.main === module) {
  install().catch((error) => {
    console.error(`Failed to install podcast-player-cli: ${error.message}`);
    process.exit(1);
  });
}

module.exports = {
  buildAssetUrl,
  install,
  resolveTarget
};
