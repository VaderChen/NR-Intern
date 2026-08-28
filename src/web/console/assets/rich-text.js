(() => {
  const sources = new WeakMap();
  const frames = new WeakMap();
  const bareResourcePattern = /https?:\/\/[^\s<>"'`]+|(?:[A-Za-z]:[\\/]|\\\\[^\\\s]+[\\/]|\/)[^\s<>"'`]+/gi;
  const trailingPunctuation = /[.,;:!?\)\]\}，。；：！？）】》」』]+$/u;

  function resourceDescriptor(value) {
    const target = (value || "").trim();
    if (/^https?:\/\/[^\s]+$/i.test(target)) return { kind: "url", target };
    if (/^(?:\/|[A-Za-z]:[\\/]|\\\\)[^\r\n]*$/.test(target)) return { kind: "path", target };
    return null;
  }

  function resourceAnchor(descriptor, label) {
    const anchor = document.createElement("a");
    anchor.className = "resource-link";
    anchor.dataset.resourceKind = descriptor.kind;
    anchor.dataset.resourceTarget = descriptor.target;
    anchor.href = descriptor.kind === "url" ? descriptor.target : "#";
    anchor.title = descriptor.kind === "url" ? "使用預設瀏覽器開啟" : "開啟本機路徑";
    anchor.setAttribute("aria-label", `${anchor.title}：${descriptor.target}`);
    anchor.append(label);
    return anchor;
  }

  function annotateMarkdownLinks(element) {
    for (const link of element.querySelectorAll("a[href]")) {
      const href = link.getAttribute("href") || "";
      if (href.startsWith("#")) continue;
      const descriptor = resourceDescriptor(href);
      if (!descriptor) {
        link.removeAttribute("href");
        continue;
      }
      link.classList.add("resource-link");
      link.dataset.resourceKind = descriptor.kind;
      link.dataset.resourceTarget = descriptor.target;
      if (descriptor.kind === "url") {
        link.title = "使用預設瀏覽器開啟";
        link.setAttribute("aria-label", `使用預設瀏覽器開啟：${descriptor.target}`);
        link.target = "_blank";
        link.rel = "noopener noreferrer";
      } else {
        link.href = "#";
        link.title = "開啟本機路徑";
        link.setAttribute("aria-label", `開啟本機路徑：${descriptor.target}`);
        link.removeAttribute("target");
        link.removeAttribute("rel");
      }
    }
  }

  function linkifyInlineCode(element) {
    for (const code of [...element.querySelectorAll("code")]) {
      if (code.closest("pre") || code.closest("a")) continue;
      const descriptor = resourceDescriptor(code.textContent);
      if (!descriptor) continue;
      code.replaceWith(resourceAnchor(descriptor, code.cloneNode(true)));
    }
  }

  function validBareResourceBoundary(value, index) {
    if (index === 0) return true;
    return /[\s\(\[\{<「『（【：:]/u.test(value[index - 1]);
  }

  function linkifyTextNode(node) {
    const value = node.nodeValue || "";
    bareResourcePattern.lastIndex = 0;
    const fragment = document.createDocumentFragment();
    let cursor = 0;
    let changed = false;
    for (let match = bareResourcePattern.exec(value); match; match = bareResourcePattern.exec(value)) {
      if (!validBareResourceBoundary(value, match.index)) continue;
      const suffix = match[0].match(trailingPunctuation)?.[0] || "";
      const candidate = suffix ? match[0].slice(0, -suffix.length) : match[0];
      const descriptor = resourceDescriptor(candidate);
      if (!descriptor) continue;
      fragment.append(value.slice(cursor, match.index));
      fragment.append(resourceAnchor(descriptor, document.createTextNode(candidate)));
      if (suffix) fragment.append(suffix);
      cursor = match.index + match[0].length;
      changed = true;
    }
    if (!changed) return;
    fragment.append(value.slice(cursor));
    node.replaceWith(fragment);
  }

  function linkifyBareResources(element) {
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    const nodes = [];
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const parent = node.parentElement;
      if (!parent || parent.closest("a, pre, code, script, style, textarea, .katex")) continue;
      nodes.push(node);
    }
    for (const node of nodes) linkifyTextNode(node);
  }

  function enhanceResources(element) {
    annotateMarkdownLinks(element);
    linkifyInlineCode(element);
    linkifyBareResources(element);
  }

  function markdownHTML(source) {
    if (typeof window.marked?.parse !== "function" || typeof window.DOMPurify?.sanitize !== "function") return null;
    try {
      const unsafeHTML = window.marked.parse(source, {
        async: false,
        breaks: true,
        gfm: true,
      });
      return window.DOMPurify.sanitize(unsafeHTML, {
        USE_PROFILES: { html: true },
        ALLOW_ARIA_ATTR: true,
        ALLOW_DATA_ATTR: false,
        FORBID_ATTR: ["style"],
        FORBID_TAGS: ["audio", "button", "embed", "form", "iframe", "img", "input", "object", "option", "picture", "script", "select", "source", "style", "textarea", "video"],
      });
    } catch (_) {
      return null;
    }
  }

  function render(element, source) {
    if (!element) return;
    const pendingFrame = frames.get(element);
    if (pendingFrame) {
      cancelAnimationFrame(pendingFrame);
      frames.delete(element);
    }
    const value = source || "";
    sources.set(element, value);
    const safeHTML = markdownHTML(value);
    if (safeHTML === null) {
      element.textContent = value;
      enhanceResources(element);
      return;
    }
    element.innerHTML = safeHTML;
    enhanceResources(element);
    if (typeof window.renderMathInElement === "function") {
      window.renderMathInElement(element, {
        delimiters: [
          { left: "$$", right: "$$", display: true },
          { left: "\\[", right: "\\]", display: true },
          { left: "\\(", right: "\\)", display: false },
          { left: "$", right: "$", display: false },
        ],
        errorCallback: () => {},
        ignoredTags: ["script", "noscript", "style", "textarea", "pre", "code", "option"],
        strict: "warn",
        throwOnError: false,
        trust: false,
      });
    }
  }

  function schedule(element, source) {
    if (!element) return;
    sources.set(element, source || "");
    if (frames.has(element)) return;
    const frame = requestAnimationFrame(() => {
      frames.delete(element);
      render(element, sources.get(element) || "");
    });
    frames.set(element, frame);
  }

  function source(element) {
    return sources.get(element) || "";
  }

  window.RichTextRenderer = Object.freeze({ markdownHTML, render, schedule, source });
})();
