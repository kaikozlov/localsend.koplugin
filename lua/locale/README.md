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
| `localsend.pot` | Translation template, regenerated from source (`just pot`). Do not edit by hand. |
| `<lang>.po` | One catalogue per language (e.g. `pt_PT.po`). Edit these. |

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

After adding or changing user-facing strings wrapped in `_()` / `deps._()` /
`N_()` / `deps.N_()`, regenerate the template and merge it into existing
translations:

```sh
just pot
msgmerge --update --backup=none lua/locale/<lang>.po lua/locale/localsend.pot
just i18n-check
```

## Requirements

The commands above require GNU gettext-tools (`xgettext`, `msginit`,
`msgmerge`, and `msgfmt`).
