const test = require("node:test");
const assert = require("node:assert/strict");

const {
  buildRequestOptions,
  getProxyForUrl,
  noProxyEntryMatches,
  shouldBypassProxy
} = require("./install");

test("prefers HTTPS_PROXY for https downloads", () => {
  const proxyUrl = getProxyForUrl("https://github.com/example/release", {
    HTTPS_PROXY: "http://secure-proxy.internal:8443",
    HTTP_PROXY: "http://fallback-proxy.internal:8080"
  });

  assert.equal(proxyUrl, "http://secure-proxy.internal:8443");
});

test("falls back to HTTP_PROXY for https downloads when HTTPS_PROXY is unset", () => {
  const proxyUrl = getProxyForUrl("https://github.com/example/release", {
    HTTP_PROXY: "http://fallback-proxy.internal:8080"
  });

  assert.equal(proxyUrl, "http://fallback-proxy.internal:8080");
});

test("respects NO_PROXY host and port matching", () => {
  assert.equal(noProxyEntryMatches("github.com", "github.com", "443"), true);
  assert.equal(noProxyEntryMatches(".github.com", "api.github.com", "443"), true);
  assert.equal(noProxyEntryMatches("github.com:8443", "github.com", "443"), false);
  assert.equal(noProxyEntryMatches("example.com", "github.com", "443"), false);
});

test("bypasses proxy when NO_PROXY covers the target host", () => {
  const bypass = shouldBypassProxy(new URL("https://api.github.com/repos"), {
    HTTPS_PROXY: "http://secure-proxy.internal:8443",
    NO_PROXY: ".github.com"
  });

  assert.equal(bypass, true);
});

test("buildRequestOptions installs a custom connection factory when proxy env is present", () => {
  const options = buildRequestOptions("https://github.com/example/release", {
    HTTPS_PROXY: "http://secure-proxy.internal:8443"
  });

  assert.equal(typeof options.createConnection, "function");
  assert.equal(options.agent, false);
});
