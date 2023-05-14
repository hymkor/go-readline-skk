go-readline-skk
================

このパッケージは Go言語製のコマンドライン向けの一行入力パッケージ [go-readline-ny] に [SKK] ライクな「かな漢字変換機能」を実現するアドオンです。

![./demo.gif](./demo.gif)

現在は試作中で、まだユーザ辞書のセーブなどはできません。

```example.go
package main

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    "github.com/hymkor/go-readline-skk"
    "github.com/nyaosorg/go-readline-ny"
)

func main() {
    customJisyo := ".skk-jisyo"
    if home, err := os.UserHomeDir(); err == nil {
        customJisyo = filepath.Join(home, customJisyo)
    }
    skk.Setup(customJisyo, "SKK-JISYO.L")

    var ed readline.Editor
    text, err := ed.ReadLine(context.Background())
    if err != nil {
        fmt.Fprintln(os.Stderr, "Error:", err.Error())
        os.Exit(1)
    }
    fmt.Println("TEXT:", text)

    skk.DumpUserJisyoUTF8(os.Stdout)
}
```

[go-readline-ny]: https://github.com/nyaosorg/go-readline-ny
[SKK]: https://ja.wikipedia.org/wiki/SKK