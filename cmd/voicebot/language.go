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
	"sleepy":        "خواب‌آلود",
	"awake":         "بیدار",
	welcome:         "سلام! 👋 این ربات برای جمع‌آوری نمونه‌های صوتی درس یادگیری ماشین آماری استاد زاهدی ساخته شده است. از هر دانشجو خواسته شده هر روز ۲۰ جفت صدا ضبط کند: در هر جفت یک پیام صوتی خواب‌آلود و یک پیام صوتی بیدار، هر کدام حدود ۵ ثانیه. در پایان ترم، این صداها مجموعه‌داده‌ای برای یک پروژه یادگیری ماشین می‌شوند. در هر دو صدا یک جمله یکسان بگویید و متن آن را یک بار برای هر جفت بنویسید. صداهای بیشتر از ۱۰ ثانیه پذیرفته نمی‌شوند. صدا و متن شما تا وقتی جفت را حذف کنید ذخیره می‌ماند. سازنده: @AliSohani2082. یکی از گزینه‌های زیر را انتخاب کنید یا منوی کنار کادر پیام را باز کنید.",
	"Record a pair": "ضبط یک جفت",
	"My pairs":      "جفت‌های من",
	"Help":          "راهنما",
	"Choose an option below or open the Menu for commands.":              "یکی از گزینه‌های زیر را انتخاب کنید یا منو را برای دیدن فرمان‌ها باز کنید.",
	"Ready for another pair?":                                            "برای ضبط جفت بعدی آماده‌اید؟",
	"Pair saved. Record another pair or view your pairs below.":          "جفت ذخیره شد. می‌توانید جفت بعدی را ضبط کنید یا جفت‌های خود را ببینید.",
	"Stopped. Choose what to do next.":                                   "متوقف شد. کار بعدی را انتخاب کنید.",
	"No submitted pairs yet. Ready to record your first pair?":           "هنوز جفتی ثبت نکرده‌اید. برای ضبط اولین جفت آماده‌اید؟",
	"Starting a new pair will discard your unfinished pair. Start over?": "با شروع جفت تازه، جفت ناتمام فعلی حذف می‌شود. از نو شروع می‌کنید؟",
	"Start over":        "شروع دوباره",
	"Keep current pair": "ادامه جفت فعلی",
	"Okay. Continue your current pair using the latest prompt, or use /cancel to discard it.":      "باشه. جفت فعلی را طبق آخرین پیام ادامه دهید یا برای حذف آن /cancel را بزنید.",
	"Please send a Telegram voice message, not a file or text. Aim for 5 seconds.":                 "لطفاً پیام صوتی تلگرام بفرستید، نه فایل یا متن. حدود ۵ ثانیه باشد.",
	"That recording must be between 1 and 10 seconds. Please try again and aim for 5 seconds.":     "مدت صدا باید بین ۱ تا ۱۰ ثانیه باشد. دوباره تلاش کنید؛ حدود ۵ ثانیه مناسب است.",
	"That recording is too large for Telegram's bot download limit. Please record a shorter note.": "حجم این صدا از محدودیت دانلود ربات تلگرام بیشتر است. لطفاً صدای کوتاه‌تری ضبط کنید.",
	"This voice note is %d seconds. Submit it or record a new one?":                                "این پیام صوتی %d ثانیه است. ثبت شود یا صدای تازه‌ای ضبط می‌کنید؟",
	"Submit":           "ثبت",
	"Record a new one": "ضبط دوباره",
	"Please type what you said (up to 1,000 characters).":                                                                           "لطفاً آنچه گفتید را بنویسید (حداکثر ۱۰۰۰ نویسه).",
	"Transcript updated. Please keep both recordings in the pair on the same sentence.":                                             "متن به‌روزرسانی شد. لطفاً هر دو صدای جفت، یک جمله یکسان داشته باشند.",
	"Now record your awake voice. Say the same sentence as in the sleepy note. Aim for 5 seconds; 10 seconds maximum.":              "حالا صدای بیدار خود را ضبط کنید. همان جمله صدای خواب‌آلود را بگویید. حدود ۵ ثانیه و حداکثر ۱۰ ثانیه باشد.",
	"Use the Submit or Record a new one button under your last recording.":                                                          "از دکمه ثبت یا ضبط دوباره زیر آخرین صدا استفاده کنید.",
	"That recording choice has expired. Follow the latest prompt.":                                                                  "این انتخاب منقضی شده است. پیام جدیدتر را دنبال کنید.",
	"Send a new %s voice note. Aim for 5 seconds.":                                                                                  "یک پیام صوتی %s تازه بفرستید. حدود ۵ ثانیه باشد.",
	"Recording replaced. Please keep both recordings in the pair on the same sentence. Edit its transcript if the wording changed.": "صدا جایگزین شد. لطفاً هر دو صدای جفت یک جمله یکسان داشته باشند. اگر جمله تغییر کرده، متن آن را هم ویرایش کنید.",
	"Type exactly what you said in that %s recording.":                                                                              "دقیقاً آنچه در صدای %s گفتید را بنویسید.",
	"Type the sentence you said in both recordings.":                                                                                "جمله‌ای را که در هر دو صدا گفتید بنویسید.",
	"Pair not found.": "جفت پیدا نشد.",
	"Finish or /cancel your current pair first.":                                        "اول جفت فعلی را تمام کنید یا /cancel را بزنید.",
	"Send a replacement %s Telegram voice note. Aim for 5 seconds; 10 seconds maximum.": "یک پیام صوتی %s جایگزین بفرستید. حدود ۵ ثانیه و حداکثر ۱۰ ثانیه باشد.",
	"Type the corrected transcript for the %s recording.":                               "متن اصلاح‌شده صدای %s را بنویسید.",
	"Type the corrected sentence for both recordings.":                                  "جملهٔ اصلاح‌شدهٔ هر دو صدا را بنویسید.",
	"Recording unavailable.":                                                            "صدا در دسترس نیست.",
	"Permanently delete this pair and both audio files?":                                "این جفت و هر دو فایل صوتی برای همیشه حذف شوند؟",
	"Delete permanently":                                                                "حذف همیشگی",
	"Keep it":                                                                           "نگه داشتن",
	"Pair deleted. If storage was temporarily unavailable, cleanup will retry when the bot restarts.":                                                                    "جفت حذف شد. اگر فضای ذخیره‌سازی موقتاً در دسترس نبوده باشد، پاک‌سازی پس از راه‌اندازی دوباره ربات تکرار می‌شود.",
	"That button is no longer valid. Use /my to see your pairs.":                                                                                                         "این دکمه دیگر معتبر نیست. برای دیدن جفت‌ها /my را بزنید.",
	"First, send a sleepy voice note. Say a sentence of your choice, aim for 5 seconds, and keep it under 10 seconds. You will say the same sentence in the awake note.": "اول یک پیام صوتی خواب‌آلود بفرستید. جمله‌ای دلخواه بگویید؛ حدود ۵ ثانیه و کمتر از ۱۰ ثانیه باشد. همین جمله را در صدای بیدار هم خواهید گفت.",
	"Nothing to cancel.":    "چیزی برای لغو کردن وجود ندارد.",
	"Page out of range.":    "شماره صفحه نامعتبر است.",
	"Previous":              "قبلی",
	"Next":                  "بعدی",
	"Your submitted pairs:": "جفت‌های ثبت‌شده شما:",
	"Pair %s · %s\nSleepy (%ds): %s\nAwake (%ds): %s\n\nBoth recordings should say the same sentence.": "جفت %s · %s\nخواب‌آلود (%d ثانیه): %s\nبیدار (%d ثانیه): %s\n\nهر دو صدا باید یک جمله یکسان داشته باشند.",
	"Pair %s · %s\nSleepy: %ds · Awake: %ds\nTranscript: %s":                                           "جفت %s · %s\nخواب‌آلود: %d ثانیه · بیدار: %d ثانیه\nمتن: %s",
	"Play sleepy":                    "پخش صدای خواب‌آلود",
	"Play awake":                     "پخش صدای بیدار",
	"Replace sleepy voice":           "جایگزینی صدای خواب‌آلود",
	"Edit sleepy text":               "ویرایش متن خواب‌آلود",
	"Replace awake voice":            "جایگزینی صدای بیدار",
	"Edit awake text":                "ویرایش متن بیدار",
	"Edit pair transcript":           "ویرایش متن جفت",
	"Delete pair":                    "حذف جفت",
	"What would you like to export?": "چه چیزی را می‌خواهید دریافت کنید؟",
	"All my records":                 "همه صداهای من",
	"This week":                      "این هفته",
	"Today":                          "امروز",
	downloadHelp:                     "با /download صداهای خودتان را به صورت فایل‌های WAV داخل یک فایل زیپ دریافت کنید.",
	"Your previous export is still being prepared. Please wait for it.":                "فایل قبلی شما هنوز در حال آماده‌سازی است. لطفاً صبر کنید.",
	"I couldn't build your export. Please try again later, or choose a shorter range.": "ساخت فایل شما ممکن نشد. لطفاً بعداً دوباره تلاش کنید یا بازه کوتاه‌تری انتخاب کنید.",
	"You have no submitted recordings in that range.":                                  "در این بازه صدای ثبت‌شده‌ای ندارید.",
	"Preparing your file… this can take a minute.":                                     "در حال آماده‌سازی فایل شما… ممکن است یک دقیقه طول بکشد.",
	"Your export is large, so it will arrive as several zip files.":                    "حجم فایل زیاد است، بنابراین در چند فایل زیپ ارسال می‌شود.",
}
