// Replaces the browser's confirm() for hx-confirm with the page's native modal
// <dialog>: same blocking semantics, but styled, with a contextual action
// label, and Cancel focused first. Without JavaScript the forms post
// directly, as before.

const dialog = document.getElementById("confirm-dialog");

if (dialog) {
  const title = dialog.querySelector("[data-confirm-title]");
  const message = dialog.querySelector("[data-confirm-message]");
  const accept = dialog.querySelector("[data-confirm-accept]");
  let pending = null;

  dialog.addEventListener("close", () => {
    const issueRequest = pending;
    pending = null;
    // Esc and other close requests leave returnValue empty: treat as cancel.
    const confirmed = dialog.returnValue === "confirm";
    dialog.returnValue = "";
    if (confirmed) issueRequest?.(true);
  });

  document.addEventListener("htmx:confirm", (event) => {
    const { question, elt, issueRequest } = event.detail;
    if (!question) return;
    event.preventDefault();
    title.textContent = elt.dataset.confirmTitle || "Please confirm";
    message.textContent = question;
    accept.textContent = elt.dataset.confirmLabel || "Confirm";
    pending = issueRequest;
    dialog.returnValue = "";
    dialog.showModal();
  });
}
