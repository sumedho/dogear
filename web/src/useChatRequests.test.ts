import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useChatRequests } from "./useChatRequests";

describe("useChatRequests", () => {
  it("tracks and stops requests independently per chat", () => {
    const { result } = renderHook(() => useChatRequests());
    const started: AbortController[] = [];
    act(() => { const first = result.current.begin("first"); const second = result.current.begin("second"); if (first) started.push(first); if (second) started.push(second); });
    expect(result.current.activeChats).toEqual(new Set(["first", "second"]));
    expect(result.current.begin("first")).toBeNull();
    act(() => result.current.stop("first"));
    expect(started[0].signal.aborted).toBe(true);
    expect(started[1].signal.aborted).toBe(false);
    act(() => result.current.finish("first", started[0]));
    expect(result.current.activeChats).toEqual(new Set(["second"]));
  });
});
