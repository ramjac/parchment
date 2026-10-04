```parchment-meta
{
  "parchment_format": "parchment-single-file-v1",
  "id": "d2",
  "kind": "document",
  "title": "Community Garden Project Brief",
  "created_at": "2026-01-15T09:00:00Z",
  "modified_at": "2026-01-15T09:00:00Z",
  "format_version": 1,
  "location": "parchment/artifacts/d2/content.md"
}
```

```parchment-document
{
  "layout": {
    "version": 1,
    "page_size": "letter",
    "orientation": "portrait",
    "margins_mm": { "top": 25, "right": 25, "bottom": 25, "left": 25 },
    "columns": 1,
    "header": "",
    "footer": "",
    "page_numbers": "center"
  },
  "images": [
    {
      "name": "image-04509606a67711eb.png",
      "data": "iVBORw0KGgoAAAANSUhEUgAAAUAAAAC0CAIAAABqhmJGAAAC9klEQVR42u3dTUrDUBSA0a6mKxAKnbkLcaLTzgTFTXUxD1yFU6eO7LAEqSY37y/vwJmmDfflI6SkyS59fgGd2hkBCBgQMCBgEDAgYEDAgIBBwICAAQGDgAEBAwIGBAwCBgQMCBgQMAgYGCzg74/n2ywJNBfwn90qGRoNeEG9Gob6AS9OV8ZQOeBV6tUwVAh4xXo1DEUDXr1eDYOAQcCV6tUwZA84a70aBgGDgAUMGwu4QL0aBgGDgAUMAhYwCBgELGAQsIBBwAIGAYOABQwCFjC4FxoQMAhYwLCVgJMnckDXASfPxAIBCxjqBJw8Fxq6Djh5MwN0HXDybiToOuBgxpYH6gecvB8Yug74/yVbEmg3YEDAgIBBwICAAQGDgAEBAwIGBAwCBgQMCBgEbAogYEDAgIBBwICAAQEDAgYBAwIGBAwCBgQMCBgQMAgYEDAgYEDAIGBAwICAQcCAgAEBAwIGAQMCBgQMCBgEDAgYEDAIGBAwIGDg94Dfz09ApwQMAgYEDAgYBAwIGOL2r8drBtJKwKf7PReTA3TWtiPMPzKf3ASMgAUsYAELWMACFnCDA7lNwK0E/Ph2iJisa/DTspp1gE62zRfww8tdmyYDqbszAhZwaM8FLGABC1jAAhawgIvUJeBtBhy5khSwgAXsDCxgAQtYwAIWsIAFXCbgWVcuAhawgJ2BBSxgAQtYwALO8UUC7oiABRwiYAELWMDjBhzcYQELWMACFnDBrpq65UvAAt5mwG3+aCxgAQtYwAJerdjgnScCFrCAnYEFLOBh/g8sYAELWMACFrCABSxgAQtYwAIWsIAFLGABC1jAAhawgAV8QMACFrCABSxgAQsYAQtYwAIWMAIWsIAFLGABC1jAAhawgAUsYAELWMACFrCAxSlgAQtYwAJGwAIWsIAFLOByAbOA+fc7fwEjYAELWMAI2AEkYPMfK2BAwCBgQMCAgAEBg4ABAQMCBgQMAgYEDAgYBAwIGBAwIGAQMCBgQMCAgEHAQBU/mxL2k2O7ASEAAAAASUVORK5CYII="
    }
  ]
}
```

<!-- parchment-body -->

# Community Garden Project Brief

## Purpose and goal

This brief shows Markdown supported by
[CommonMark](https://spec.commonmark.org/0.31.2/ "version 0.31.2") in the
document body. It is still ordinary Markdown: **bold**, *emphasis*,
***both together***, _alternate emphasis_, __alternate bold__, and `inline
code` remain readable in any compatible editor.

Turn the unused lot on Cedar Street into a shared garden with accessible paths,
raised beds, and a shaded seating area.

The garden is a place for everyone.\
This line ends with a hard break; this next sentence follows on a new line.

An ampersand can be written as `&amp;` (&amp;), and a plant can be shown with
a numeric character reference: &#x1F331;.

Escapes keep punctuation literal: \*not emphasis\*, \[not a link\], and \#
not a heading. In a code span, use double backticks to include a backtick:
`` ` ``.

## Plan and materials

1. Confirm access and water with the property owner.
2. Invite neighbors to a weekend cleanup.
3. Build the first four beds:
   * Set out the cedar boards.
   + Fill each bed with compost and soil.
4. Publish a volunteer schedule.

The first weekend has a few simple jobs:

- Clear litter and mark the accessible route.

- Prepare the beds:

  1) Measure and assemble the frames.
  2) Leave room for the paved path.

- Plant seeds and label each row.

The list above is loose because its items contain paragraphs and a nested list.
This continuation line belongs to the same item.

> **Access first:** keep the route level and wide enough for a wheelchair.
>
> Ask the property owner before changing the water connection.
The water tap must remain clear during the work.
>
> > A nested quote can preserve a note from the planning meeting.
>
>     Bring gloves, a trowel, and a water bottle.

A short setext heading
----------------------

This paragraph follows a setext heading. The next thematic break separates
the plan from the measures.

***

A level-one setext heading
=========================

This heading uses the other setext underline.

## Success measures

- At least twelve households participate in the first season.
- Keep one bed reachable from the paved path.
- Share a short harvest update with the neighborhood each month.

For background, see the [project brief][brief], the [brief][] again, or the
shortcut reference [brief]. An inline link can include a destination title:
[CommonMark's home page](https://commonmark.org/ "CommonMark").

![Raised beds in the garden plan](image-04509606a67711eb.png "Garden plan")

The same embedded illustration also works as a reference image:

![Garden beds and accessible path][garden-plan]

An automatic link recognizes <https://example.org/garden> and an email address
such as <garden@example.org>. Raw inline HTML is also part of CommonMark:
<kbd>Ctrl</kbd> is a planning shortcut, and <span>inline markup</span> stays
in the source.

### Fenced and indented code

Fenced blocks can carry an info string:

```text
Bed 1: herbs
Bed 2: tomatoes
```

Tildes can fence a block too:

~~~json
{"beds": 4, "accessible_path": true}
~~~

Four leading spaces create an indented code block:

    water = "shared tap"
    volunteers = 12

### Raw HTML blocks

CommonMark leaves HTML blocks available when a document needs them.

<section>
  <p>A block-level HTML element can contain ordinary HTML.</p>
</section>

<!-- A comment block is preserved in the Markdown source. -->

<?planning note="processing instruction block"?>

<!DOCTYPE garden-brief>

<![CDATA[Garden notes can include a CDATA block.]]>

<script>
const beds = 4;
</script>

<custom-garden-note>
  A custom element is an HTML block as well.
</custom-garden-note>

#### Fourth-level heading
##### Fifth-level heading
###### Sixth-level heading

[brief]: https://example.org/garden-brief "Community garden brief"
[garden-plan]: image-04509606a67711eb.png "Raised-bed plan"
