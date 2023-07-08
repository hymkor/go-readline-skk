package skk

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	rl "github.com/nyaosorg/go-readline-ny"
	"github.com/nyaosorg/go-readline-ny/keys"
	// "github.com/nyaosorg/go-windows-dbg"
)

func debug(text string) {
	// dbg.Println(text)
}

const (
	markerWhite = "▽"
	markerBlack = "▼"
)

type _Trigger struct {
	Key byte
	M   *Mode
}

func (trig *_Trigger) String() string {
	return "SKK_HENKAN_TRIGGER_" + string(trig.Key)
}

type QueryPrompter interface {
	Prompt(io.Writer, string) (int, error)
	LineFeed(io.Writer) (int, error)
	Recurse(string) QueryPrompter
}

type QueryOnNextLine struct{}

func (QueryOnNextLine) Prompt(w io.Writer, prompt string) (int, error) {
	return fmt.Fprintf(w, "\n%s ", prompt)
}

func (QueryOnNextLine) LineFeed(w io.Writer) (int, error) {
	return io.WriteString(w, "\r\x1B[K\x1B[A")
}

func (q QueryOnNextLine) Recurse(originalPrompt string) QueryPrompter {
	return &QueryOnCurrentLine{OriginalPrompt: originalPrompt}
}

type QueryOnCurrentLine struct {
	OriginalPrompt string
}

func (q *QueryOnCurrentLine) Prompt(w io.Writer, prompt string) (int, error) {
	return fmt.Fprintf(w, "\r%s ", prompt)
}

func (q *QueryOnCurrentLine) LineFeed(w io.Writer) (int, error) {
	return fmt.Fprintf(w, "\r%s \x1B[K", q.OriginalPrompt)
}

func (q *QueryOnCurrentLine) Recurse(originalPrompt string) QueryPrompter {
	return &QueryOnCurrentLine{OriginalPrompt: originalPrompt}
}

func (M *Mode) ask1(B *rl.Buffer, prompt string) (string, error) {
	M.QueryPrompter.Prompt(B.Out, prompt)
	B.Out.Flush()
	rc, err := B.GetKey()
	M.QueryPrompter.LineFeed(B.Out)
	B.RepaintAfterPrompt()
	return rc, err
}

func (M *Mode) ask(ctx context.Context, B *rl.Buffer, prompt string, ime bool) (string, error) {
	inputNewWord := &rl.Editor{
		PromptWriter: func(w io.Writer) (int, error) {
			return M.QueryPrompter.Prompt(w, prompt)
		},
		Writer: B.Writer,
		LineFeedWriter: func(_ rl.Result, w io.Writer) (int, error) {
			return M.QueryPrompter.LineFeed(w)
		},
	}
	if ime {
		m := &Mode{
			User:          M.User,
			System:        M.System,
			QueryPrompter: M.QueryPrompter.Recurse(prompt),
		}
		m.backupKeyMap(&inputNewWord.KeyMap)
		m.enableHiragana(inputNewWord)
	}
	defer B.RepaintAfterPrompt()
	return inputNewWord.ReadLine(ctx)
}

// Mode is an instance of SKK. It contains system dictionaries and user dictionaries.
type Mode struct {
	User          Jisyo
	System        Jisyo
	QueryPrompter QueryPrompter
	saveMap       []rl.Command
	kana          *_Kana
}

var rxNumber = regexp.MustCompile(`[0-9]+`)

var rxToNumber = regexp.MustCompile(`#[0123459]`)

var kansuji = map[rune]string{
	'0': "〇",
	'1': "一",
	'2': "二",
	'3': "三",
	'4': "四",
	'5': "五",
	'6': "六",
	'7': "七",
	'8': "八",
	'9': "九",
}

func numberToKanji(s string) string {
	var buffer strings.Builder
	for _, r := range s {
		buffer.WriteString(kansuji[r])
	}
	return buffer.String()
}

func hanToZenString(s string) string {
	var buffer strings.Builder
	for _, r := range s {
		buffer.WriteRune(hanToZen(r))
	}
	return buffer.String()
}

func (M *Mode) _lookup(source string) ([]string, bool) {
	list, ok := M.User[source]
	if ok {
		return list, true
	}
	list, ok = M.System[source]
	return list, ok
}

func (M *Mode) lookup(source string) ([]string, bool) {
	list, ok := M._lookup(source)
	if ok {
		return list, ok
	}
	loc := rxNumber.FindStringIndex(source)
	if loc == nil {
		return nil, false
	}
	number := source[loc[0]:loc[1]]
	source = source[:loc[0]] + "#" + source[loc[1]:]
	list, ok = M._lookup(source)
	if !ok {
		return nil, false
	}
	newList := make([]string, 0, len(list))
	for _, s := range list {
		tmp := rxToNumber.ReplaceAllStringFunc(s, func(ss string) string {
			switch ss[1] {
			case '0': // 無変換
				return number
			case '1': // 全角化
				return hanToZenString(number)
			case '2': // 漢数字で位取りあり
				return numberToKanji(number)
			case '3': // 漢数字で位取りなし
				return numberToKanji(number) // あとでやる
			default:
				return number
			}
		})
		newList = append(newList, tmp)
	}
	return newList, true
}

func (M *Mode) newCandidate(ctx context.Context, B *rl.Buffer, source string) (string, bool) {
	newWord, err := M.ask(ctx, B, source, true)
	B.RepaintAfterPrompt()
	if err != nil || len(newWord) <= 0 {
		return "", false
	}
	list, _ := M.lookup(source)

	// 二重登録よけ
	for _, candidate := range list {
		if candidate == newWord {
			return newWord, true
		}
	}
	// リストの先頭に挿入
	list = append(list, "")
	copy(list[1:], list)
	list[0] = newWord
	M.User[source] = list
	return newWord, true
}

const listingStartIndex = 4

func (M *Mode) henkanMode(ctx context.Context, B *rl.Buffer, markerPos int, source string, postfix string) rl.Result {
	list, found := M.lookup(source)
	if !found {
		// 辞書登録モード
		result, ok := M.newCandidate(ctx, B, source)
		if ok {
			// 新変換文字列を展開する
			B.ReplaceAndRepaint(markerPos, result)
			return rl.CONTINUE
		} else {
			// 変換前に一旦戻す
			B.ReplaceAndRepaint(markerPos, markerWhite+source)
			return rl.CONTINUE
		}
	}
	current := 0
	candidate, _, _ := strings.Cut(list[current], ";")
	B.ReplaceAndRepaint(markerPos, markerBlack+candidate+postfix)
	for {
		input, _ := B.GetKey()
		if input == string(keys.CtrlG) {
			B.ReplaceAndRepaint(markerPos, markerWhite+source)
			return rl.CONTINUE
		} else if input < " " {
			removeOne(B, markerPos)
			return rl.CONTINUE
		} else if input == " " {
			current++
			if current >= len(list) {
				// 辞書登録モード
				result, ok := M.newCandidate(ctx, B, source)
				if ok {
					// 新変換文字列を展開する
					B.ReplaceAndRepaint(markerPos, result)
					return rl.CONTINUE
				} else {
					// 変換前に一旦戻す
					B.ReplaceAndRepaint(markerPos, markerWhite+source)
					return rl.CONTINUE
				}
			}
			if current >= listingStartIndex {
				for {
					var buffer strings.Builder
					_current := current
					for _, key := range "ASDFJKL:" {
						if _current >= len(list) {
							break
						}
						candidate, _, _ = strings.Cut(list[_current], ";")
						fmt.Fprintf(&buffer, "%c:%s ", key, candidate)
						_current++
					}
					fmt.Fprintf(&buffer, "[残り %d]", len(list)-_current)
					key, err := M.ask1(B, buffer.String())
					if err == nil {
						if index := strings.Index("asdfjkl:", key); index >= 0 {
							candidate, _, _ = strings.Cut(list[current+index], ";")
							B.ReplaceAndRepaint(markerPos, candidate)
							return rl.CONTINUE
						} else if key == " " {
							current = _current
						} else if key == "x" {
							current -= len("ASDFJKL:")
							if current < listingStartIndex {
								if current < 0 {
									current = 0
								}
								break
							}
						} else if key == string(keys.CtrlG) {
							B.ReplaceAndRepaint(markerPos, markerWhite+source)
							return rl.CONTINUE
						}
					}
				}
			} else {
				candidate, _, _ = strings.Cut(list[current], ";")
				B.ReplaceAndRepaint(markerPos, markerBlack+candidate+postfix)
			}
		} else if input == "x" {
			current--
			if current < 0 {
				B.ReplaceAndRepaint(markerPos, markerWhite+source)
				return rl.CONTINUE
			}
			candidate, _, _ = strings.Cut(list[current], ";")
			B.ReplaceAndRepaint(markerPos, markerBlack+candidate+postfix)
		} else if input == "X" {
			prompt := fmt.Sprintf(`really purge "%s /%s/ "?(yes or no)`, source, list[current])
			ans, err := M.ask(ctx, B, prompt, false)
			if err == nil {
				if ans == "y" || ans == "yes" {
					// 本当はシステム辞書を参照しないようLisp構文を
					// セットしなければいけないが、そこまではしない.
					if len(list) <= 1 {
						delete(M.User, source)
					} else {
						if current+1 < len(list) {
							copy(list[current:], list[current+1:])
						}
						list = list[:len(list)-1]
						M.User[source] = list
					}
					B.ReplaceAndRepaint(markerPos, "")
					return rl.CONTINUE
				}
			}
		} else {
			removeOne(B, markerPos)
			return eval(ctx, B, input)
		}
	}
}

func (trig *_Trigger) Call(ctx context.Context, B *rl.Buffer) rl.Result {
	if markerPos := seekMarker(B); markerPos >= 0 {
		// 送り仮名つき変換
		var source strings.Builder
		source.WriteString(B.SubString(markerPos+1, B.Cursor))
		source.WriteByte(trig.Key)

		var postfix string
		if index := strings.IndexByte("aiueo", trig.Key); index >= 0 {
			postfix = trig.M.kana.table[string(trig.Key)]
		} else {
			postfix = string(trig.Key)
		}
		return trig.M.henkanMode(ctx, B, markerPos, source.String(), postfix)
	}
	B.InsertAndRepaint(markerWhite)
	r := &_Romaji{kana: trig.M.kana, last: string(trig.Key)}
	return r.Call(ctx, B)
}

func seekMarker(B *rl.Buffer) int {
	for i := B.Cursor - 1; i >= 0; i-- {
		ch := B.Buffer[i].String()
		if ch == markerWhite || ch == markerBlack {
			return i
		}
	}
	return -1
}

func removeOne(B *rl.Buffer, pos int) {
	copy(B.Buffer[pos:], B.Buffer[pos+1:])
	B.Buffer = B.Buffer[:len(B.Buffer)-1]
	B.Cursor--
	B.RepaintAfterPrompt()
}

func (M *Mode) cmdStartHenkan(ctx context.Context, B *rl.Buffer) rl.Result {
	markerPos := seekMarker(B)
	if markerPos < 0 {
		B.InsertAndRepaint(" ")
		return rl.CONTINUE
	}
	source := B.SubString(markerPos+1, B.Cursor)

	return M.henkanMode(ctx, B, markerPos, source, "")
}

func eval(ctx context.Context, B *rl.Buffer, input string) rl.Result {
	return B.LookupCommand(input).Call(ctx, B)
}

func (M *Mode) cmdKakutei(ctx context.Context, B *rl.Buffer) rl.Result {
	markerPos := seekMarker(B)
	if markerPos < 0 {
		return M.cmdLatinMode(ctx, B)
	}
	// kakutei
	removeOne(B, markerPos)
	return rl.CONTINUE
}

func (M *Mode) cmdCancel(ctx context.Context, B *rl.Buffer) rl.Result {
	markerPos := seekMarker(B)
	if markerPos < 0 {
		return M.cmdLatinMode(ctx, B)
	}
	B.ReplaceAndRepaint(markerPos, "")
	return rl.CONTINUE
}

func (m *Mode) cmdToggleKana(_ context.Context, B *rl.Buffer) rl.Result {
	m.kana = kanaTable[m.kana.switchTo]
	m.kana.enableRomaji(B, m)
	return rl.CONTINUE
}

func (M *Mode) cmdAbbrevMode(ctx context.Context, B *rl.Buffer) rl.Result {
	if seekMarker(B) >= 0 {
		return rl.CONTINUE
	}
	M.restoreKeyMap(&B.KeyMap)
	B.InsertAndRepaint(markerWhite)
	B.BindKey(" ", &rl.GoCommand{
		Name: "SKK_ABBREV_START_HENKAN",
		Func: func(ctx context.Context, B *rl.Buffer) rl.Result {
			rc := M.cmdStartHenkan(ctx, B)
			M.cmdEnableRomaji(ctx, B)
			return rc
		},
	})
	return rl.CONTINUE
}

type canBindKey interface {
	BindKey(keys.Code, rl.Command)
	LookupCommand(string) rl.Command
}

func (K *_Kana) enableRomaji(X canBindKey, mode *Mode) {
	for i := range romajiTrigger {
		c := romajiTrigger[i : i+1]
		X.BindKey(keys.Code(c), &_Romaji{kana: K, last: c})
	}
	X.BindKey("q", &rl.GoCommand{Name: "SKK_TOGGLE_KANA", Func: mode.cmdToggleKana})
	X.BindKey("/", &rl.GoCommand{Name: "SKK_ABBREV_MODE", Func: mode.cmdAbbrevMode})

	const upperRomaji = "AIUEOKSTNHMYRWFGZDBPCJ"
	for i, c := range upperRomaji {
		u := &_Trigger{Key: byte(unicode.ToLower(c)), M: mode}
		X.BindKey(keys.Code(upperRomaji[i:i+1]), u)
	}
}

func (M *Mode) enableHiragana(X canBindKey) {
	debug("enableHiragana")
	M.kana = hiragana
	hiragana.enableRomaji(X, M)
	X.BindKey(" ", &rl.GoCommand{Name: "SKK_START_HENKAN", Func: M.cmdStartHenkan})
	X.BindKey("l", &rl.GoCommand{Name: "SKK_LATIN_MODE", Func: M.cmdLatinMode})
	X.BindKey("L", &rl.GoCommand{Name: "SKK_JISX0208_LATIN_MODE", Func: M.cmdJis0208LatinMode})
	X.BindKey(keys.CtrlG, &rl.GoCommand{Name: "SKK_CANCEL", Func: M.cmdCancel})
	X.BindKey(keys.CtrlJ, &rl.GoCommand{Name: "SKK_KAKUTEI", Func: M.cmdKakutei})
}

func (M *Mode) backupKeyMap(km *rl.KeyMap) {
	if M.saveMap != nil {
		return
	}
	debug("backupKeyMap")
	M.saveMap = make([]rl.Command, 0, 0x80)
	for i := '\x00'; i <= '\x80'; i++ {
		key := keys.Code(string(i))
		val, _ := km.Lookup(key)
		M.saveMap = append(M.saveMap, val)
	}
}

func (M *Mode) restoreKeyMap(km *rl.KeyMap) {
	debug("restoreKeyMap")
	for i, command := range M.saveMap {
		km.BindKey(keys.Code(string(rune(i))), command)
	}
}

func (M *Mode) cmdEnableRomaji(ctx context.Context, B *rl.Buffer) rl.Result {
	debug("cmdEnableRomaji")
	M.backupKeyMap(&B.KeyMap)
	M.enableHiragana(B)
	return rl.CONTINUE
}

func (M *Mode) cmdLatinMode(ctx context.Context, B *rl.Buffer) rl.Result {
	debug("cmdLatinMode")
	M.restoreKeyMap(&B.KeyMap)
	return rl.CONTINUE
}

func hanToZen(c rune) rune {
	if c < ' ' || c >= '\x7f' {
		return c
	}
	if c == ' ' {
		return '　'
	}
	return c - ' ' + '\uFF00'
}

func (M *Mode) cmdJis0208LatinMode(ctx context.Context, B *rl.Buffer) rl.Result {
	for i := rune(' '); i < '\x7F'; i++ {
		z := string(hanToZen(i))
		B.BindKey(keys.Code(string(i)), &rl.GoCommand{
			Name: "SKK_JISX0208_LATIN_INSERT_" + z,
			Func: func(_ context.Context, B *rl.Buffer) rl.Result {
				B.InsertAndRepaint(z)
				return rl.CONTINUE
			}})
	}
	B.BindKey(keys.CtrlJ, &rl.GoCommand{
		Name: "SKK_JISX0208_LATIN_KAKUTEI",
		Func: func(ctx context.Context, B *rl.Buffer) rl.Result {
			M.restoreKeyMap(&B.Editor.KeyMap)
			return M.cmdEnableRomaji(ctx, B)
		},
	})
	return rl.CONTINUE
}
