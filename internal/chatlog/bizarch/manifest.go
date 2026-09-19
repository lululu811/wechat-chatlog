package bizarch

import "fmt"

// ClipperManifest is the recommended Obsidian Web Clipper template
// (YAML). Users paste it into the Clipper template editor at
// https://obsidian.md/clipper (or via the local Clipper popup).
//
// Why this shape:
//   - The trigger pin matches mp.weixin.qq.com only (Clipper also
//     supports youtube/twitter/etc, those should not auto-clip).
//   - noteFolder keeps each public account in its own subfolder so
//     the vault doesn't get a single huge "Clippings" pile.
//   - noteName includes the date and the author so users can sort
//     by date in Obsidian's file explorer.
//   - properties capture chatlog-friendly metadata so future tooling
//     (Dataview, Templater, etc.) can filter on it.
//
// The "ghID" property is read from a <meta name="gh-id"> tag the
// chatlog WeChat db does not actually inject — Clipper will
// gracefully leave it empty if the meta tag is absent, which is the
// right default.
const ClipperManifest = `name: 公众号归档 (chatlog biz2md)
noteName: '{{author}}-{{publishedDate | date("YYYY-MM-DD")}}-{{title}}'
noteFolder: '公众号/{{author}}'
schema: |
  ---
  ghID: '{{selector:meta[name="gh-id"]}}'
  url: '{{url}}'
  author: '{{selector:#js_name}}'
  publishedAt: '{{selector:#publish_time}}'
  source: chatlog-biz2md
  tags:
    - 公众号
    - chatlog
  ---
properties:
  - name: ghID
    selector: 'meta[name="gh-id"]'
    attribute: content
  - name: author
    selector: '#js_name'
  - name: publishedAt
    selector: '#publish_time'
triggers:
  - url: 'https://mp.weixin.qq.com/s'
behavior:
  download: true
  format: markdown
  captureOriginalContent: false
`

// PrintManifest writes ClipperManifest to stdout. The user can then
// pipe it to a file or copy-paste into the Clipper template editor.
//
// We deliberately print the manifest verbatim (no "saving..." banner)
// so it can be redirected cleanly:
//
//   chatlog biz2md --manifest > /tmp/clipper.yaml
func PrintManifest() {
	fmt.Print(ClipperManifest)
}