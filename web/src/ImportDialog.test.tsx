import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { importMarkdown } from "./api";
import { ImportDialog } from "./ImportDialog";

vi.mock("./api", () => ({ importMarkdown: vi.fn() }));
const importMock = vi.mocked(importMarkdown);

describe("ImportDialog", () => {
  beforeEach(() => importMock.mockReset());

  it("retries only failed files", async () => {
    importMock.mockRejectedValueOnce(new Error("bad file")).mockResolvedValue({ documents: 1, chunks: 2, images: 0, warnings: [] });
    const { container } = render(<ImportDialog onClose={() => {}} onImported={async () => {}} />);
    const files = [new File(["# One"], "one.md", { type: "text/markdown" }), new File(["# Two"], "two.md", { type: "text/markdown" })];
    fireEvent.change(container.querySelector('input[type="file"]')!, { target: { files } });
    fireEvent.click(screen.getByRole("button", { name: "Import" }));
    await screen.findByRole("button", { name: "Retry failed" });
    expect(importMock).toHaveBeenCalledTimes(2);
    importMock.mockResolvedValueOnce({ documents: 1, chunks: 1, images: 0, warnings: [] });
    fireEvent.click(screen.getByRole("button", { name: "Retry failed" }));
    await waitFor(() => expect(importMock).toHaveBeenCalledTimes(3));
    expect(importMock.mock.calls[2][0].name).toBe("one.md");
  });
});
