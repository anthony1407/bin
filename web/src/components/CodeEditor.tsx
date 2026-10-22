import { lazy, Suspense, type ComponentProps } from "react";
import type MonacoEditor from "@monaco-editor/react";

// Monaco is several megabytes: it loads the first time an editor is shown.
const LazyEditor = lazy(() => import("../lib/monaco"));

/** The Monaco code editor, bundled locally and loaded on demand. */
export default function Editor(props: ComponentProps<typeof MonacoEditor>) {
  return (
    <Suspense fallback={<div className="flex h-full items-center justify-center text-sm text-muted">Loading editor…</div>}>
      <LazyEditor {...props} />
    </Suspense>
  );
}
