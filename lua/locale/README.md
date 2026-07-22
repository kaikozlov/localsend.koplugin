# Translations

This directory holds the gettext catalogues for the LocalSend KOReader plugin.
The plugin ships its own translations, independent of KOReader's core language
packs. The UI language follows KOReader's **Language** setting automatically.
Any string without a plugin translation falls back through KOReader's catalogue
and then to English.

KOReader manages its translations with standard GNU gettext catalogues and
Weblate. Until LocalSend has its own Weblate component, translations are
contributed as `.po` files through pull requests.

## Files

| File | Purpose |
| ---- | ------- |
| `localsend.pot` | Translation template, regenerated from source by `just pot`. Do not edit by hand. |
| `<lang>.po` | One catalogue per language (e.g. `pt_PT.po`). Translate its `msgstr` values; `just pot` maintains its entries, ordering, and source references. |

The plugin reads plain-text `.po` catalogues directly at runtime, so no compiled
`.mo` is shipped.

## Adding a language

No code changes required.

1. Create the catalogue with `msginit`, using the locale code from KOReader's
   language list:

   ```sh
   msginit --no-translator --locale=pt_PT \
     --input=lua/locale/localsend.pot \
     --output-file=lua/locale/pt_PT.po
   ```

   A gettext-aware editor such as Poedit, Lokalize, or Virtaal can be used
   instead. Verify the generated `Language` and `Plural-Forms` headers; some
   `msginit` versions normalize `pt_PT` to `pt`, while KOReader and the plugin
   catalogue filename use `pt_PT`.
2. Fill in the `msgstr` values. Preserve placeholders such as `%1`; plural
   entries use `msgstr[0]`, `msgstr[1]`, and any additional forms required by
   the language.
3. Run `just i18n-check`, then commit the `.po` file. Catalogues are bundled
   into releases automatically.

## Updating strings

After adding, changing, or removing user-facing strings wrapped in `_()` /
`deps._()` / `N_()` / `deps.N_()`, run:

```sh
just pot
just i18n-check
```

`just pot` performs the complete update: it regenerates `localsend.pot` from
`lua/*.lua`, then runs `msgmerge --update` for every `lua/locale/*.po`
catalogue. New entries are added with empty `msgstr` values, removed entries
are dropped, and ordering plus source references follow the template. Existing
translations and translator metadata are preserved.

The read-only checks run automatically in the pre-commit hook and CI via
`just verify-static`:

- `pot-check` regenerates the template in a temporary directory and fails if
  the committed `localsend.pot` differs from the source strings.
- `i18n-check` validates every `.po`, merges it against the template in a
  temporary directory, and fails on any difference—including missing or
  obsolete entries, ordering, and source references.

These checks enforce synchronization, not translation completeness. A newly
added entry may have an empty `msgstr` and fall back to English until translated.
When either check reports drift, `just pot` is the single command that fixes it.

## Requirements

The `just` recipes run GNU gettext tools (`xgettext`, `msginit`, `msgmerge`, and
`msgfmt`) inside the pinned development image; no host gettext installation is
required.
