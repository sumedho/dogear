import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { embeddingIndexStatus, getSettings } from "./api";
import { SettingsDialog } from "./SettingsDialog";

vi.mock("./api", () => ({
  buildEmbeddingIndex: vi.fn(), embeddingIndexStatus: vi.fn(), getSettings: vi.fn(), saveSettings: vi.fn(), testSettings: vi.fn(),
}));

describe("SettingsDialog", () => {
  it("protects unsaved changes and disables index builds", async () => {
    vi.mocked(getSettings).mockResolvedValue({ provider: { base_url: "http://chat", model: "chat", timeout: "30s", api_key_set: false }, embedding: { base_url: "http://embed", model: "embed", timeout: "30s", api_key_set: false, dimensions: 32, batch_size: 8, query_instruction: "" }, environment_overrides: [] });
    vi.mocked(embeddingIndexStatus).mockResolvedValue({ configured: true, complete: false, stale: true, model: "embed", dimensions: 32, indexed: 0, total: 1 });
    const close = vi.fn();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<SettingsDialog onClose={close} onNotify={() => {}} onChanged={() => {}} />);
    const models = await screen.findAllByLabelText("Model", { selector: "input" });
    fireEvent.change(models[0], { target: { value: "new-chat" } });
    expect(screen.getByText(/Unsaved changes/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Build embeddings" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Close settings" }));
    expect(confirm).toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
    confirm.mockRestore();
  });
});
