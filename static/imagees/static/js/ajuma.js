/* Ajuma Fashion Hub — one small enhancement, and nothing depends on it.
 *
 * The shop works with JavaScript switched off: pressing "Order on WhatsApp"
 * posts the form, and the server composes the message and redirects to wa.me.
 * All this file does is redraw the preview bubble as the buyer changes size,
 * quantity or note — and, in the studio, as the owner edits the template.
 *
 * A statically exported shop has no server to post to, so there the button is
 * a plain link carrying the default message. This file then also keeps that
 * link up to date, and reveals the size and quantity fields, which the export
 * leaves hidden so that a page without scripting never offers a choice it
 * cannot pass on.
 *
 * The rules below mirror BuildOrderMessage in whatsapp.go, which remains the
 * one that writes the message on the live shop.
 */
(function () {
  'use strict';

  var MAX_MESSAGE = 1800; // matches maxMessageRunes
  var MAX_NOTE = 400;     // matches maxNoteRunes
  var MAX_QTY = 20;       // matches maxQuantity

  function clamp(text, limit) {
    var runes = Array.from(text);
    return runes.length <= limit ? text : runes.slice(0, limit).join('').trim();
  }

  // collapseBlankLines: runs of empty lines become one, so a placeholder that
  // resolved to nothing leaves no hole behind.
  function collapse(text) {
    var lines = text.replace(/\r\n/g, '\n').split('\n');
    var out = [];
    var blank = 0;
    for (var i = 0; i < lines.length; i++) {
      var line = lines[i].replace(/[ \t]+$/, '');
      if (line === '') {
        if (++blank > 1) continue;
      } else {
        blank = 0;
      }
      out.push(line);
    }
    return out.join('\n').replace(/^\n+|\n+$/g, '');
  }

  // The buyer's choices, one labelled line each — OrderRequest.OrderLines.
  function orderLines(panel) {
    var lines = [];

    var size = panel.querySelector('input[name="size"]:checked, select[name="size"]');
    if (size && size.value.trim()) lines.push('Size: ' + size.value.trim());

    var qtyField = panel.querySelector('[name="qty"]');
    var qty = qtyField ? parseInt(qtyField.value, 10) : 1;
    if (!(qty > 0)) qty = 1;
    if (qty > MAX_QTY) qty = MAX_QTY;
    if (qty > 1) lines.push('Quantity: ' + qty);

    var notes = panel.querySelector('[name="notes"]');
    var note = notes ? clamp(notes.value.trim(), MAX_NOTE) : '';
    if (note) lines.push('Note: ' + note);

    return lines.join('\n');
  }

  function compose(payload, template, panel) {
    var options = panel ? orderLines(panel) : '';
    if (payload.soldOut && payload.soldOutNote) {
      options = options ? options + '\n' + payload.soldOutNote : payload.soldOutNote;
    }

    var values = Object.assign({}, payload.values, { options: options });
    var filled = template.replace(/\{([a-z]+)\}/g, function (whole, token) {
      return Object.prototype.hasOwnProperty.call(values, token) ? values[token] : whole;
    });
    return clamp(collapse(filled), MAX_MESSAGE);
  }

  // A form on the live shop, a plain block on an exported page.
  document.querySelectorAll('[data-order]').forEach(function (panel) {
    var payload;
    try {
      payload = JSON.parse(panel.dataset.order);
    } catch (err) {
      return; // leave the server-rendered preview exactly as it is
    }
    if (!payload || !payload.values) return;

    var preview = document.getElementById('wa-preview');
    if (!preview) return;

    // In the studio the template being edited wins; in the shop it is fixed.
    var editor = document.getElementById('message-template');
    var choices = editor ? null : panel;

    // The export hides these until it is known they can be acted on.
    var fields = panel.querySelector('[data-choices]');
    if (fields) fields.hidden = false;

    // Present only on an exported page: there the whole address is rebuilt
    // here. encodeURIComponent leaves a few more characters alone than Go's
    // url.QueryEscape does; WhatsApp accepts either.
    var link = panel.querySelector('a[data-order-link]');

    var redraw = function () {
      var template = editor ? editor.value : payload.template;
      if (!template.trim()) template = payload.fallback;
      var message = compose(payload, template, choices);
      preview.textContent = message;
      if (link && payload.waBase) {
        link.href = payload.waBase + encodeURIComponent(message);
      }
    };

    panel.addEventListener('input', redraw);
    panel.addEventListener('change', redraw);
    redraw();
  });
})();
