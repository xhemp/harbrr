import { afterEach, describe, expect, it } from "vitest"
import { getApiBaseUrl, getBaseUrl, urlHasExplicitPort } from "./base-url"

describe("getBaseUrl", () => {
  afterEach(() => {
    delete window.__HARBRR_BASE_URL__
  })

  const cases: { name: string, injected: string | undefined, want: string }[] = [
    { name: "unset (dev server)", injected: undefined, want: "" },
    { name: "empty (root deploy)", injected: "", want: "" },
    { name: "bare slash normalizes to empty", injected: "/", want: "" },
    { name: "subpath", injected: "/harbrr", want: "/harbrr" },
    { name: "subpath with trailing slash", injected: "/harbrr/", want: "/harbrr" },
  ]

  for (const c of cases) {
    it(c.name, () => {
      if (c.injected !== undefined) window.__HARBRR_BASE_URL__ = c.injected
      expect(getBaseUrl()).toBe(c.want)
      expect(getApiBaseUrl()).toBe(`${c.want}/api`)
    })
  }
})

describe("urlHasExplicitPort", () => {
  const cases: { name: string, url: string, want: boolean }[] = [
    { name: "explicit non-default port", url: "http://harbrr:7478", want: true },
    // The URL parser normalizes away a port written explicitly as the
    // scheme's own default (https:443 here), so it reads the same as if
    // no port had been given at all.
    { name: "port written as the scheme default normalizes to no port", url: "https://harbrr:443", want: false },
    { name: "no port, https (typical reverse-proxy origin)", url: "https://harbrr.example.com", want: false },
    { name: "no port, http", url: "http://harbrr.example.com", want: false },
    { name: "path with no port", url: "https://harbrr.example.com/base", want: false },
    { name: "unparseable string", url: "not a url", want: false },
  ]

  for (const c of cases) {
    it(c.name, () => {
      expect(urlHasExplicitPort(c.url)).toBe(c.want)
    })
  }
})
