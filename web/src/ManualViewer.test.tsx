import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { listDocumentChunks, loadDocumentChunks, searchManual } from "./api";
import { ManualViewer } from "./ManualViewer";
import type { DocumentChunk } from "./types";

vi.mock("./api", () => ({ listDocumentChunks: vi.fn(), loadDocumentChunks: vi.fn(), searchManual: vi.fn() }));
const loadMock = vi.mocked(loadDocumentChunks);
const listMock = vi.mocked(listDocumentChunks);
const searchMock = vi.mocked(searchManual);
const chunk = (id: number): DocumentChunk => ({ id, document_id: "manual", ordinal: id, heading_path: `Section ${id}`, heading_level: 2, page_number: id, start_line: id, end_line: id + 1, text: `Text ${id}` });

describe("ManualViewer", () => {
  beforeEach(() => { loadMock.mockReset(); listMock.mockReset(); searchMock.mockReset(); });

  it("loads another page without duplicating sections", async () => {
    loadMock.mockResolvedValue(Array.from({ length: 50 }, (_, index) => chunk(index + 1)));
    listMock.mockResolvedValue([chunk(50), chunk(51)]);
    render(<ManualViewer documentId="manual" onAsk={() => {}} onClose={() => {}} />);
    await screen.findAllByText("Section 1");
    fireEvent.click(screen.getAllByRole("button", { name: "Load more sections" })[0]);
    await screen.findAllByText("Section 51");
    expect(screen.getAllByText("Section 50")).toHaveLength(2);
    expect(listMock).toHaveBeenCalledWith("manual", 50);
  });

  it("searches within the active manual", async () => {
    loadMock.mockResolvedValue([]);
    searchMock.mockResolvedValue([{ chunk_id: 9, document_id: "manual", title: "Manual", heading_path: "MIDI", page_number: 4, start_line: 1, end_line: 2, snippet: "clock", score: 1 }]);
    render(<ManualViewer documentId="manual" onAsk={() => {}} onClose={() => {}} />);
    fireEvent.change(screen.getByRole("searchbox"), { target: { value: "clock" } });
    await waitFor(() => expect(searchMock).toHaveBeenCalledWith("clock", "manual"));
    expect(await screen.findByText("MIDI")).toBeInTheDocument();
  });
});
