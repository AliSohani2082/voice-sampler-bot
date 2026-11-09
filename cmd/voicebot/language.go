package main

import "context"

type languageKey struct{}

func withLanguage(ctx context.Context, language string) context.Context {
	return context.WithValue(ctx, languageKey{}, language)
}

func translate(ctx context.Context, english string) string {
	if ctx.Value(languageKey{}) != "fa" {
		return english
	}
	if persian, ok := fa[english]; ok {
		return persian
	}
	return english
}

func translateKind(ctx context.Context, kind string) string {
	if kind == "sleepy" {
		return translate(ctx, "sleepy")
	}
	return translate(ctx, "awake")
}

var fa = map[string]string{
	"sleepy": "خواب‌آلود",
	"awake":  "بیدار",
	"Send one sleepy and one awake Telegram voice note per pair. Aim for 5 seconds; clips over 10 seconds are rejected. Say the same sentence in both notes and type what you said for each. I store the audio and text until you delete the pair. Use /new to start, /my to view or edit your pairs, and /cancel to stop an unfinished pair.": "برای هر جفت، یک پیام صوتی خواب‌آلود و یک پیام صوتی بیدار بفرستید. بهتر است هر کدام حدود ۵ ثانیه باشد؛ صدای بیشتر از ۱۰ ثانیه پذیرفته نمی‌شود. در هر دو صدا یک جمله یکسان بگویید و متن آن را برای هر کدام بنویسید. صدا و متن را تا زمانی که جفت را حذف کنید نگه می‌دارم. برای شروع /new، برای دیدن یا ویرایش جفت‌ها /my و برای توقف جفت ناتمام /cancel را بزنید.",
	"Use /new, /my, or /cancel.":                                                                   "از /new، /my یا /cancel استفاده کنید.",
	"Use /new to start a pair or /my to see previous pairs.":                                       "برای شروع یک جفت /new و برای دیدن جفت‌های قبلی /my را بزنید.",
	"Please send a Telegram voice message, not a file or text. Aim for 5 seconds.":                 "لطفاً پیام صوتی تلگرام بفرستید، نه فایل یا متن. حدود ۵ ثانیه باشد.",
	"That recording must be between 1 and 10 seconds. Please try again and aim for 5 seconds.":     "مدت صدا باید بین ۱ تا ۱۰ ثانیه باشد. دوباره تلاش کنید؛ حدود ۵ ثانیه مناسب است.",
	"That recording is too large for Telegram's bot download limit. Please record a shorter note.": "حجم این صدا از محدودیت دانلود ربات تلگرام بیشتر است. لطفاً صدای کوتاه‌تری ضبط کنید.",
	"This voice note is %d seconds. Submit it or record a new one?":                                "این پیام صوتی %d ثانیه است. ثبت شود یا صدای تازه‌ای ضبط می‌کنید؟",
	"Submit":           "ثبت",
	"Record a new one": "ضبط دوباره",
	"Please type what you said (up to 1,000 characters).":                                                                           "لطفاً آنچه گفتید را بنویسید (حداکثر ۱۰۰۰ نویسه).",
	"Transcript updated. Please keep both recordings in the pair on the same sentence.":                                             "متن به‌روزرسانی شد. لطفاً هر دو صدای جفت، یک جمله یکسان داشته باشند.",
	"Now record your awake voice. Say the same sentence as in the sleepy note. Aim for 5 seconds; 10 seconds maximum.":              "حالا صدای بیدار خود را ضبط کنید. همان جمله صدای خواب‌آلود را بگویید. حدود ۵ ثانیه و حداکثر ۱۰ ثانیه باشد.",
	"Pair saved. Use /new for another pair or /my to view, edit, or delete your pairs.":                                             "جفت ذخیره شد. برای جفت تازه /new و برای دیدن، ویرایش یا حذف جفت‌ها /my را بزنید.",
	"Use the Submit or Record a new one button under your last recording.":                                                          "از دکمه ثبت یا ضبط دوباره زیر آخرین صدا استفاده کنید.",
	"That recording choice has expired. Follow the latest prompt.":                                                                  "این انتخاب منقضی شده است. پیام جدیدتر را دنبال کنید.",
	"Send a new %s voice note. Aim for 5 seconds.":                                                                                  "یک پیام صوتی %s تازه بفرستید. حدود ۵ ثانیه باشد.",
	"Recording replaced. Please keep both recordings in the pair on the same sentence. Edit its transcript if the wording changed.": "صدا جایگزین شد. لطفاً هر دو صدای جفت یک جمله یکسان داشته باشند. اگر جمله تغییر کرده، متن آن را هم ویرایش کنید.",
	"Type exactly what you said in that %s recording.":                                                                              "دقیقاً آنچه در صدای %s گفتید را بنویسید.",
	"Pair not found.": "جفت پیدا نشد.",
	"Finish or /cancel your current pair first.":                                        "اول جفت فعلی را تمام کنید یا /cancel را بزنید.",
	"Send a replacement %s Telegram voice note. Aim for 5 seconds; 10 seconds maximum.": "یک پیام صوتی %s جایگزین بفرستید. حدود ۵ ثانیه و حداکثر ۱۰ ثانیه باشد.",
	"Type the corrected transcript for the %s recording.":                               "متن اصلاح‌شده صدای %s را بنویسید.",
	"Recording unavailable.":                                                            "صدا در دسترس نیست.",
	"Permanently delete this pair and both audio files?":                                "این جفت و هر دو فایل صوتی برای همیشه حذف شوند؟",
	"Delete permanently":                                                                "حذف همیشگی",
	"Keep it":                                                                           "نگه داشتن",
	"Pair deleted. If storage was temporarily unavailable, cleanup will retry when the bot restarts.":                                                                    "جفت حذف شد. اگر فضای ذخیره‌سازی موقتاً در دسترس نبوده باشد، پاک‌سازی پس از راه‌اندازی دوباره ربات تکرار می‌شود.",
	"That button is no longer valid. Use /my to see your pairs.":                                                                                                         "این دکمه دیگر معتبر نیست. برای دیدن جفت‌ها /my را بزنید.",
	"First, send a sleepy voice note. Say a sentence of your choice, aim for 5 seconds, and keep it under 10 seconds. You will say the same sentence in the awake note.": "اول یک پیام صوتی خواب‌آلود بفرستید. جمله‌ای دلخواه بگویید؛ حدود ۵ ثانیه و کمتر از ۱۰ ثانیه باشد. همین جمله را در صدای بیدار هم خواهید گفت.",
	"Nothing to cancel.": "چیزی برای لغو کردن وجود ندارد.",
	"Stopped. Use /new whenever you want to start a new pair.": "متوقف شد. هر وقت خواستید جفت تازه‌ای بسازید /new را بزنید.",
	"Page out of range.": "شماره صفحه نامعتبر است.",
	"Previous":           "قبلی",
	"Next":               "بعدی",
	"No submitted pairs yet. Use /new to start.": "هنوز جفتی ثبت نشده است. برای شروع /new را بزنید.",
	"Your submitted pairs:":                      "جفت‌های ثبت‌شده شما:",
	"Pair %s · %s\nSleepy (%ds): %s\nAwake (%ds): %s\n\nBoth recordings should say the same sentence.": "جفت %s · %s\nخواب‌آلود (%d ثانیه): %s\nبیدار (%d ثانیه): %s\n\nهر دو صدا باید یک جمله یکسان داشته باشند.",
	"Play sleepy":          "پخش صدای خواب‌آلود",
	"Play awake":           "پخش صدای بیدار",
	"Replace sleepy voice": "جایگزینی صدای خواب‌آلود",
	"Edit sleepy text":     "ویرایش متن خواب‌آلود",
	"Replace awake voice":  "جایگزینی صدای بیدار",
	"Edit awake text":      "ویرایش متن بیدار",
	"Delete pair":          "حذف جفت",
}
