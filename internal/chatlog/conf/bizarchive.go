package conf

// BizArchive holds configuration for the `chatlog biz2md` command.
// biz2md coordinates between chatlog's local sqlite and an external
// Obsidian Web Clipper (browser extension) for archiving public
// account articles into an Obsidian vault.
//
// All fields are optional. Empty values fall back to defaults
// resolved at runtime (see internal/chatlog/bizarch).
type BizArchive struct {
	// VaultPath is the absolute path to the Obsidian vault root.
	// Used by `--status` to count md files actually present in the vault.
	VaultPath string `mapstructure:"vault_path"`

	// QueuePath is the absolute path of the JSON queue file written by
	// `--queue`. The default is ~/chatlog-biz-clipper-queue.json.
	QueuePath string `mapstructure:"queue_path"`

	// ClipperTemplate is the template name inside Obsidian Web Clipper
	// that should be used when clipping mp.weixin.qq.com articles.
	// Stored only so `--manifest` can echo the user's chosen name.
	ClipperTemplate string `mapstructure:"clipper_template"`
}