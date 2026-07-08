import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { useEffect, type ReactNode } from "react"
import { useTestIndexer } from "./useIndexers"

const { toastSuccess, toastError, testIndexerMock } = vi.hoisted(() => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  testIndexerMock: vi.fn(),
}))
vi.mock("sonner", () => ({
  toast: { success: toastSuccess, error: toastError },
}))
vi.mock("@/lib/api", () => ({
  api: { testIndexer: testIndexerMock },
}))

function wrap(children: ReactNode) {
  return (
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  )
}

// Stands in for AddIndexerSheet's save-and-test flow: fire the mutation, then
// the caller unmounts it immediately (mirroring onClose() running right after
// test.mutate() in AddIndexerSheet.tsx, before the test settles).
function SaveAndTestProbe({ slug }: { slug: string }) {
  const test = useTestIndexer({ toastResult: true })
  const { mutate } = test
  // eslint-disable-next-line react-hooks/exhaustive-deps -- fire once, like the real save-and-test call site
  useEffect(() => { mutate(slug) }, [])
  return null
}

describe("useTestIndexer toastResult", () => {
  beforeEach(() => {
    toastSuccess.mockClear()
    toastError.mockClear()
    testIndexerMock.mockReset()
  })

  it("toasts a failed test even after the triggering component unmounts", async () => {
    let resolve!: (v: { ok: boolean, error?: string }) => void
    testIndexerMock.mockReturnValue(new Promise((r) => { resolve = r }))

    const { unmount } = render(wrap(<SaveAndTestProbe slug="mam" />))
    unmount()

    resolve({ ok: false, error: "bad passkey" })

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("mam: test failed — bad passkey"))
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it("toasts a passed test even after the triggering component unmounts", async () => {
    let resolve!: (v: { ok: boolean, error?: string }) => void
    testIndexerMock.mockReturnValue(new Promise((r) => { resolve = r }))

    const { unmount } = render(wrap(<SaveAndTestProbe slug="mam" />))
    unmount()

    resolve({ ok: true })

    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("mam: test passed"))
    expect(toastError).not.toHaveBeenCalled()
  })

  it("toasts a transport failure even after the triggering component unmounts", async () => {
    let reject!: (e: Error) => void
    testIndexerMock.mockReturnValue(new Promise((_r, rj) => { reject = rj }))

    const { unmount } = render(wrap(<SaveAndTestProbe slug="mam" />))
    unmount()

    reject(new Error("network down"))

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("mam: test request failed"))
    expect(toastSuccess).not.toHaveBeenCalled()
  })
})
