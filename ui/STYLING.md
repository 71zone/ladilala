# Styling (shadcn-vue + Tailwind v4, native macOS language)

- **Kit:** shadcn-vue (Reka UI + Tailwind v4, style reka-vega, base colour zinc, icons @lucide/vue). `components.json` is committed; generated files in `src/components/ui/` are owned code, edit freely.
- **Add a component:** `bunx --bun shadcn-vue@latest add <name>` (e.g. `add hover-card`). Docs: https://www.shadcn-vue.com. Install the *Tailwind CSS IntelliSense* VS Code extension.
- **Variables:** `src/style.css`. `:root` is light, `.dark` is dark: --background (content), --card (grouped rows), --muted (window), --sidebar, --border, --primary (one system blue), --link (blue text), --muted-foreground (secondary label), --success/--warning/--destructive (state only). Radius scale: --radius 6px (controls), --radius-card 10px. Type scale: 13px body, 12 secondary, 11 caption.
- **Controls contrast:** --input (control borders) and --switch-off/--switch-on keep switches, checkboxes and field borders at 3:1 or better in both modes. Reka exposes state as data-state, so the short variants (data-checked, data-open, data-active, data-horizontal...) are mapped in style.css; keep them when regenerating components.
- **Simulate menu:** prototype-only popover in the sidebar footer (opens to the right, covers nothing at rest).
- **Dark mode:** `.dark` on `<html>`, driven by VueUse `useDark` in `src/theme.ts`; toggle in the sidebar footer.
- **Vibrancy (the only translucency):** `vibrancy-sidebar` on the Sidebar and `vibrancy-toolbar` on `Toolbar.vue`: flat tint, 20px blur, hairline border, opaque fallback under `prefers-reduced-transparency`. Everything else is opaque. No gradients anywhere.
- **macOS patterns (src/components/mac/):**
  - `Toolbar`: 52px window toolbar with title and right-aligned controls.
  - `Group` + `Row`: System Settings grouped inset list (10px container, rows 40px, hairlines, helper text below).
  - `Segmented`: shadcn ToggleGroup as a segmented control. `Pick`: shadcn Select as pop-up button. `Confirm`: AlertDialog for destructive actions.
  - Source list: shadcn Sidebar in App.vue (28px items, 6px selection pill, count badges).
  - Finder list: shadcn Table with `table-fixed` and fixed column widths, 28px rows; selected row = `data-state="selected"`.
- **Command palette:** Ctrl+K (`CommandPalette.vue`). Toasts: Sonner via `notify()` in `store.ts`.
- **New screen:** add `src/views/Foo.vue` (Toolbar first, then a `min-h-0 flex-1 overflow-auto` body), register it in `main.ts`, add an entry in `App.vue` and `CommandPalette.vue`.
