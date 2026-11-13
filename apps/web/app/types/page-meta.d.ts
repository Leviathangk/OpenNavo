// Page metadata: definePageMeta({ rail }) selects the layout rail (05 §11.2; desktop uses named rail views).
declare module '#app' {
  interface PageMeta {
    rail?: 'discover' | 'developer-map';
  }
}

export {};
