#!/usr/bin/env node

const fs = require("node:fs");
const net = require("node:net");
const path = require("node:path");
const https = require("node:https");
const tls = require("node:tls");
const { URL } = require("node:url");

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

function getProxyForUrl(rawUrl, env = process.env) {
  const targetUrl = new URL(rawUrl);
  if (shouldBypassProxy(targetUrl, env)) {
    return "";
  }

  const keys =
    targetUrl.protocol === "https:"
      ? ["HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"]
      : ["HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"];

  for (const key of keys) {
    const value = (env[key] || "").trim();
    if (value) {
      return value;
    }
  }

  return "";
}

function shouldBypassProxy(targetUrl, env = process.env) {
  const rawNoProxy = (env.NO_PROXY || env.no_proxy || "").trim();
  if (!rawNoProxy) {
    return false;
  }
  if (rawNoProxy === "*") {
    return true;
  }

  const hostname = targetUrl.hostname.toLowerCase();
  const port = targetUrl.port || (targetUrl.protocol === "https:" ? "443" : "80");

  return rawNoProxy
    .split(",")
    .map((entry) => entry.trim().toLowerCase())
    .filter(Boolean)
    .some((entry) => noProxyEntryMatches(entry, hostname, port));
}

function noProxyEntryMatches(entry, hostname, port) {
  const normalizedEntry = entry.startsWith(".") ? entry.slice(1) : entry;
  const [entryHost, entryPort] = normalizedEntry.split(":");
  if (entryPort && entryPort !== port) {
    return false;
  }

  return hostname === entryHost || hostname.endsWith(`.${entryHost}`);
}

function buildRequestOptions(rawUrl, env = process.env) {
  const targetUrl = new URL(rawUrl);
  const proxyUrl = getProxyForUrl(rawUrl, env);
  if (!proxyUrl) {
    return {
      protocol: targetUrl.protocol,
      hostname: targetUrl.hostname,
      port: Number(targetUrl.port || 443),
      path: `${targetUrl.pathname}${targetUrl.search}`
    };
  }

  return {
    protocol: targetUrl.protocol,
    hostname: targetUrl.hostname,
    port: Number(targetUrl.port || 443),
    path: `${targetUrl.pathname}${targetUrl.search}`,
    agent: false,
    createConnection: () => createProxiedTlsSocket(targetUrl, new URL(proxyUrl))
  };
}

function createProxiedTlsSocket(targetUrl, proxyUrl) {
  const proxyPort = Number(proxyUrl.port || (proxyUrl.protocol === "https:" ? 443 : 80));
  const proxyReadyEvent = proxyUrl.protocol === "https:" ? "secureConnect" : "connect";
  const connectToProxy =
    proxyUrl.protocol === "https:"
      ? () =>
          tls.connect({
            host: proxyUrl.hostname,
            port: proxyPort,
            servername: proxyUrl.hostname
          })
      : () =>
          net.connect({
            host: proxyUrl.hostname,
            port: proxyPort
          });

  return new Promise((resolve, reject) => {
    const proxySocket = connectToProxy();

    const onError = (error) => {
      proxySocket.destroy();
      reject(error);
    };

    proxySocket.once("error", onError);
    proxySocket.once(proxyReadyEvent, () => {
      const auth =
        proxyUrl.username || proxyUrl.password
          ? `Proxy-Authorization: Basic ${Buffer.from(
              `${decodeURIComponent(proxyUrl.username)}:${decodeURIComponent(proxyUrl.password)}`
            ).toString("base64")}\r\n`
          : "";
      const targetPort = Number(targetUrl.port || 443);
      const connectRequest =
        `CONNECT ${targetUrl.hostname}:${targetPort} HTTP/1.1\r\n` +
        `Host: ${targetUrl.hostname}:${targetPort}\r\n` +
        auth +
        "Connection: close\r\n\r\n";

      proxySocket.write(connectRequest);
    });

    let responseBuffer = "";
    proxySocket.on("data", (chunk) => {
      responseBuffer += chunk.toString("latin1");
      const headerEnd = responseBuffer.indexOf("\r\n\r\n");
      if (headerEnd === -1) {
        return;
      }

      const statusLine = responseBuffer.slice(0, responseBuffer.indexOf("\r\n"));
      if (!statusLine.includes(" 200 ")) {
        proxySocket.destroy();
        reject(new Error(`Proxy tunnel failed: ${statusLine}`));
        return;
      }

      proxySocket.removeListener("error", onError);
      proxySocket.removeAllListeners("data");

      const secureSocket = tls.connect({
        socket: proxySocket,
        servername: targetUrl.hostname
      });
      secureSocket.once("secureConnect", () => resolve(secureSocket));
      secureSocket.once("error", reject);
    });
  });
}

function download(url, destination) {
  return new Promise((resolve, reject) => {
    const request = https.get(buildRequestOptions(url), (response) => {
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
  buildRequestOptions,
  getProxyForUrl,
  install,
  noProxyEntryMatches,
  resolveTarget,
  shouldBypassProxy
};
