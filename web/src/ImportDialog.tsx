import { useMemo, useState } from "react";
import { importMarkdown } from "./api";
import { useDialog } from "./useDialog";

type ImportFileStatus = { state: "ready" | "importing" | "success" | "error"; detail: string };
const importFileKey = (file: File) => `${file.name}-${file.size}-${file.lastModified}`;

export function ImportDialog({ onClose, onImported }: { onClose(): void; onImported(): Promise<void> }) {
  const [files, setFiles] = useState<File[]>([]);
  const [dragging, setDragging] = useState(false);
  const [replace, setReplace] = useState(false);
  const [metadata, setMetadata] = useState({ id: "", brand: "", model: "", tags: "" });
  const [statuses, setStatuses] = useState<Record<string, ImportFileStatus>>({});
  const [selectionError, setSelectionError] = useState("");
  const [busy, setBusy] = useState(false);
  const totalSize = useMemo(() => files.reduce((sum, file) => sum + file.size, 0), [files]);
  const completed = files.filter((file) => ["success", "error"].includes(statuses[importFileKey(file)]?.state)).length;
  const failed = files.filter((file) => statuses[importFileKey(file)]?.state === "error").length;
  const succeeded = files.filter((file) => statuses[importFileKey(file)]?.state === "success").length;
  const dialogRef = useDialog<HTMLDivElement>(onClose, !busy);

  const selectFiles = (selected: File[]) => {
    const markdown = selected.filter((file) => /\.(?:md|markdown)$/i.test(file.name) || file.type === "text/markdown");
    setFiles(markdown);
    setStatuses(Object.fromEntries(markdown.map((file) => [importFileKey(file), { state: "ready", detail: "Ready" }])));
    setSelectionError(markdown.length === selected.length ? "" : "Only Markdown files can be imported.");
  };

  const runImport = async () => {
    setBusy(true);
    const pending = files.filter((file) => statuses[importFileKey(file)]?.state !== "success");
    for (let index = 0; index < pending.length; index++) {
      const file = pending[index];
      const key = importFileKey(file);
      setStatuses((value) => ({ ...value, [key]: { state: "importing", detail: `Importing ${index + 1} of ${pending.length}…` } }));
      try {
        const result = await importMarkdown(file, replace, { ...metadata, id: files.length === 1 ? metadata.id.trim() : "", brand: metadata.brand.trim(), model: metadata.model.trim(), tags: metadata.tags.trim() });
        const imageDetail = result.images === 1 ? "1 image" : `${result.images} images`;
        const warningDetail = result.warnings?.length ? ` · ${result.warnings.length} warning${result.warnings.length === 1 ? "" : "s"}` : "";
        setStatuses((value) => ({ ...value, [key]: { state: "success", detail: `${result.chunks} chunks · ${imageDetail}${warningDetail}` } }));
      } catch (error) {
        setStatuses((value) => ({ ...value, [key]: { state: "error", detail: error instanceof Error ? error.message : "Import failed" } }));
      }
    }
    try {
      await onImported();
    } catch (error) {
      setSelectionError(error instanceof Error ? `Files imported, but the manual list could not refresh: ${error.message}` : "Files imported, but the manual list could not refresh.");
    } finally {
      setBusy(false);
    }
  };

  return <div className="modal-backdrop" role="presentation"><div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-label="Import Markdown">
    <div className="modal-header"><div><h2>Import Markdown</h2><p>Embedded PNG, JPEG, GIF, and WebP images are stored with their source sections.</p></div><button className="icon-button" onClick={onClose} disabled={busy} aria-label="Close import dialog">×</button></div>
    <div className="import-metadata">
      <label>Document ID<input value={metadata.id} disabled={busy || files.length > 1} placeholder="Generated from title when empty" onChange={(event) => setMetadata({ ...metadata, id: event.target.value })} /><small>{files.length > 1 ? "A custom ID can only be used when importing one file." : "Stable identifier used by links and replacement imports."}</small></label>
      <label>Brand<input value={metadata.brand} disabled={busy} placeholder="e.g. Elektron" onChange={(event) => setMetadata({ ...metadata, brand: event.target.value })} /></label>
      <label>Model<input value={metadata.model} disabled={busy} placeholder="e.g. Analog Four MKII" onChange={(event) => setMetadata({ ...metadata, model: event.target.value })} /></label>
      <label>Tags<input value={metadata.tags} disabled={busy} placeholder="synthesizer, reference, studio" onChange={(event) => setMetadata({ ...metadata, tags: event.target.value })} /><small>Separate tags with commas.</small></label>
    </div>
    <label className={`file-drop${dragging ? " dragging" : ""}`} onDragEnter={(event) => { event.preventDefault(); setDragging(true); }} onDragOver={(event) => { event.preventDefault(); event.dataTransfer.dropEffect = "copy"; setDragging(true); }} onDragLeave={(event) => { event.preventDefault(); if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false); }} onDrop={(event) => { event.preventDefault(); event.stopPropagation(); setDragging(false); selectFiles(Array.from(event.dataTransfer.files)); }}>
      <input type="file" accept=".md,.markdown,text/markdown" multiple onChange={(event) => selectFiles(Array.from(event.target.files || []))} />
      <strong>{dragging ? "Drop Markdown files here" : "Choose or drop Markdown files"}</strong><span>Up to 100 MiB per upload</span>
    </label>
    {selectionError && <div className="message-error">{selectionError}</div>}
    {files.length > 0 && <><div className="import-progress"><progress max={files.length} value={completed} /><span>{busy ? `${completed} of ${files.length} complete` : completed === files.length ? `${succeeded} imported${failed ? ` · ${failed} failed` : ""}` : `${files.length} selected`}</span></div>
      <div className="import-list">{files.map((file) => { const status = statuses[importFileKey(file)] || { state: "ready", detail: "Ready" }; return <div className={`import-${status.state}`} key={importFileKey(file)}><span><strong>{file.name}</strong> <small>{(file.size / 1024).toFixed(1)} KiB</small></span><span className="import-detail">{status.state === "success" ? "✓ " : status.state === "error" ? "! " : ""}{status.detail}</span></div>; })}</div></>}
    <label className="checkbox"><input type="checkbox" checked={replace} onChange={(event) => setReplace(event.target.checked)} /> Replace manuals with matching IDs</label>
    <div className="modal-footer"><span>{files.length ? `${files.length} file${files.length > 1 ? "s" : ""} · ${(totalSize / 1024 / 1024).toFixed(1)} MiB` : "No files selected"}</span><button onClick={() => void runImport()} disabled={!files.length || busy || (completed === files.length && failed === 0)}>{busy ? "Importing…" : failed ? "Retry failed" : completed === files.length ? "Imported" : "Import"}</button></div>
  </div></div>;
}
