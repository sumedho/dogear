import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AnswerMarkdown } from "./AnswerMarkdown";
import type { ChatMessage } from "./types";

describe("AnswerMarkdown", () => {
  it("turns known citations into manual navigation buttons", () => {
    const onOpen = vi.fn();
    const message: ChatMessage = { id: "a", role: "assistant", status: "done", content: "Use local control [1].", sources: [{
      chunk_id: 12, label: "[1]", document_id: "dx7", title: "DX7", heading_path: "MIDI", page_number: 8,
      start_line: 20, end_line: 30, score: 1,
    }] };
    render(<AnswerMarkdown message={message} onOpen={onOpen} />);
    fireEvent.click(screen.getByRole("button", { name: "[1]" }));
    expect(onOpen).toHaveBeenCalledWith("dx7", 12);
  });

  it("opens external links without replacing the application", () => {
    render(<AnswerMarkdown message={{ id: "a", role: "assistant", content: "[Help](https://example.com)" }} onOpen={() => {}} />);
    expect(screen.getByRole("link", { name: "Help" })).toHaveAttribute("target", "_blank");
    expect(screen.getByRole("link", { name: "Help" })).toHaveAttribute("rel", "noopener noreferrer");
  });
});
