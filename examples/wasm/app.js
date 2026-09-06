const runtimeStatus = document.querySelector("#runtime-status");
const fileInput = document.querySelector("#file-input");
const extractFileButton = document.querySelector("#extract-file");
const extractHTMLButton = document.querySelector("#extract-html");
const htmlInput = document.querySelector("#html-input");
const markdownOutput = document.querySelector("#markdown-output");
const copyOutputButton = document.querySelector("#copy-output");
const documentDetails = document.querySelector("#document-details");
const detailsOutput = document.querySelector("#details-output");

let runtimeReady = false;

function setStatus(message, state = "working") {
  runtimeStatus.textContent = message;
  runtimeStatus.dataset.state = state;
}

function enableControls() {
  runtimeReady = true;
  fileInput.disabled = false;
  extractFileButton.disabled = !fileInput.files.length;
  extractHTMLButton.disabled = false;
}

async function instantiateGo() {
  const go = new Go();
  let instance;

  try {
    const result = await WebAssembly.instantiateStreaming(
      fetch("extract.wasm"),
      go.importObject,
    );
    instance = result.instance;
  } catch (streamingError) {
    console.warn("Streaming WASM instantiation failed; using ArrayBuffer fallback.", streamingError);
    const response = await fetch("extract.wasm");
    if (!response.ok) {
      throw new Error(`Could not load extract.wasm (${response.status})`);
    }
    const result = await WebAssembly.instantiate(
      await response.arrayBuffer(),
      go.importObject,
    );
    instance = result.instance;
  }

  go.run(instance).catch((error) => {
    console.error(error);
    setStatus("Go runtime stopped", "error");
  });

  if (!globalThis.goExtract) {
    throw new Error("Go runtime started without exposing goExtract");
  }
}

function summarize(doc) {
  return {
    name: doc.name,
    format: doc.format,
    mediaType: doc.mediaType,
    metadata: doc.metadata,
    attachments: (doc.attachments ?? []).map((attachment) => ({
      name: attachment.name,
      mediaType: attachment.mediaType,
      inline: attachment.inline,
      size: attachment.size,
      metadata: attachment.metadata,
      error: attachment.error,
      document: attachment.document ? summarize(attachment.document) : undefined,
    })),
  };
}

async function extract(name, mediaType, data) {
  if (!runtimeReady) return;

  setStatus("Extracting…");
  await new Promise(requestAnimationFrame);

  try {
    const result = await globalThis.goExtract.extract(name, mediaType, data);
    if (!result || typeof result !== "object") {
      throw new Error("The Go runtime returned no extraction result. Check the browser console for a Go panic.");
    }
    if (result.error) throw new Error(result.error);
    if (!result.document) throw new Error("The Go runtime returned an empty document result.");

    markdownOutput.textContent = result.document.markdown || "(No text extracted)";
    detailsOutput.textContent = JSON.stringify(summarize(result.document), null, 2);
    documentDetails.hidden = false;
    copyOutputButton.disabled = false;
    setStatus(`Extracted ${result.document.format.toUpperCase()}`, "ready");
  } catch (error) {
    markdownOutput.textContent = error.message;
    documentDetails.hidden = true;
    copyOutputButton.disabled = true;
    setStatus("Extraction failed", "error");
    console.error(error);
  }
}

fileInput.addEventListener("change", () => {
  extractFileButton.disabled = !runtimeReady || !fileInput.files.length;
});

extractFileButton.addEventListener("click", async () => {
  const [file] = fileInput.files;
  if (!file) return;
  await extract(file.name, file.type, new Uint8Array(await file.arrayBuffer()));
});

extractHTMLButton.addEventListener("click", async () => {
  await extract(
    "sample.html",
    "text/html; charset=utf-8",
    new TextEncoder().encode(htmlInput.value),
  );
});

copyOutputButton.addEventListener("click", async () => {
  await navigator.clipboard.writeText(markdownOutput.textContent);
  copyOutputButton.textContent = "Copied";
  setTimeout(() => {
    copyOutputButton.textContent = "Copy";
  }, 1200);
});

try {
  await instantiateGo();
  enableControls();
  setStatus("Go ready", "ready");
} catch (error) {
  console.error(error);
  setStatus("Could not load Go", "error");
  markdownOutput.textContent = error.message;
}
