package social

// AI-assisted composing: two independent capabilities layered on the
// shared internal/ai chat-completions client (the same one that powers the
// generic "Rephrase" button already available on this textarea) —
// generating a full post from a short topic, and adapting a single draft
// into a platform-tailored version (respecting X's character limit, adding
// hashtags for Instagram/TikTok). Both degrade the same way Rephrase does:
// ai.ErrNotConfigured surfaces as a 503 the compose page shows as an alert.

import "fmt"

const generateSystemPrompt = "You are a social media copywriter embedded in a business app. Given a short topic or a few bullet points from the user, write a complete, ready-to-publish social media post about it. Keep it concise, engaging, and in plain language with no markdown. Reply with ONLY the post text and nothing else — no quotes, no preamble, no hashtags unless they're clearly asked for."

// platformAdaptSystemPrompt tailors the same base instruction per
// platform's real constraints — X's hard character limit, and the
// hashtag convention Instagram/TikTok captions commonly use.
func platformAdaptSystemPrompt(platform string) string {
	base := fmt.Sprintf("You are a social media copywriter adapting one draft post so it reads naturally on %s. Keep the same core message and tone as the original. Reply with ONLY the adapted post text and nothing else — no quotes, no preamble, no explanation.", PlatformLabel(platform))
	switch platform {
	case "x":
		return base + " X posts have a hard 280 character limit — the adapted text MUST be 280 characters or fewer, trimming or rewording as needed rather than truncating mid-sentence."
	case "instagram":
		return base + " End the caption with 3-6 relevant, specific hashtags (lowercase, no spaces within a hashtag, each prefixed with #) on their own line."
	case "tiktok":
		return base + " Keep it short and punchy, since it's read quickly under a video, and end with 2-4 relevant hashtags on their own line."
	default:
		return base
	}
}
