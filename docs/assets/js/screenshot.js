// Lightbox for the screenshot shortcode: a plain click on a screenshot opens
// its full-size image in a modal <dialog>. Modified clicks (new tab or
// window) keep the link behaviour, and without JavaScript the link opens the
// image itself. The dialog closes with Esc, its Close button, or a click on
// the backdrop (closedby="any").

for (const link of document.querySelectorAll("a[data-fm-lightbox]")) {
  const dialog = document.getElementById(link.dataset.fmLightbox);
  if (!(dialog instanceof HTMLDialogElement)) continue;
  link.addEventListener("click", (event) => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    dialog.showModal();
  });
}

// Safari does not support closedby yet. Only the pointer position tells a
// backdrop click apart: the <dialog> is also the event target for its own
// padding. Press and release must both be outside, so a drag that ends on
// the backdrop does not close it.
if (!("closedBy" in HTMLDialogElement.prototype)) {
  const outside = (event, dialog) => {
    const r = dialog.getBoundingClientRect();
    return event.clientX < r.left || event.clientX > r.right || event.clientY < r.top || event.clientY > r.bottom;
  };
  for (const dialog of document.querySelectorAll('dialog.fm-lightbox[closedby="any"]')) {
    let pressedOutside = false;
    dialog.addEventListener("pointerdown", (event) => { pressedOutside = outside(event, dialog); });
    dialog.addEventListener("pointerup", (event) => {
      if (pressedOutside && outside(event, dialog)) dialog.close();
    });
  }
}
