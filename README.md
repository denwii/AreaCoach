# Area Coach

**Area Coach finds the tablet area for osu! (stable and lazer) from how you really aim.**
It has two sections:

- **Replay**: as soon as you finish a map, it reads the replay osu! just saved and tells
  you how to move and resize your area. One button puts it in OpenTabletDriver.
- **Live**: for practice. While you play, it reshapes the area a little at a time, map
  after map, with a soft sound at every touch, like sculpting. It remembers how you play
  at each width, day after day, finds by itself the band of widths that suit you, and
  shows the best area so far with a reliability bar that grows as you play.

The Replay advice comes from one play alone, worked out with plain geometry. Live adds a
memory of how each width went for you, so that it settles where you press closest to the
centres of the circles.

![The advice after a map](docs/screenshots/replay.png)

- [How the advice is worked out](#how-the-advice-is-worked-out)
- [The Replay section](#the-replay-section)
- [The Live section](#the-live-section)
- [Settings](#settings)
- [What it reads and changes](#what-it-reads-and-changes)
- [Requirements and install](#requirements-and-install)
- [Troubleshooting](#troubleshooting)
- [Build from source](#build-from-source)
- [License and credits](#license-and-credits)

## How the advice is worked out

1. **Every click of the play.** For each circle and slider head: where the circle was,
   how big it was, and where your pen was on the tablet when you pressed (from the
   cursor in the replay and the area you played with).
2. **Every candidate area.** Your height / width ratio stays fixed, your rotation too;
   the width can be anything in your width range, the area anywhere on the tablet. For
   each candidate, the circles are put on the tablet where that area would put them,
   just as big as you had them, and it counts how many of your pen presses land inside.
3. **The best one.** The area with the most presses inside the circles. Among areas
   about as good, the one with the presses closest to the centres: if you always press a
   little to the right of the circles, the area moves so that your presses land on the
   centre. Misses always weigh far more than that: moving a hundred presses from 0.6 to
   0.4 of the radius from the centre is worth one circle hit, and the advice never has
   more misses than the area you played with.
4. **How sure.** The same search is done again on the clicks drawn at random from the
   play: how much the answer moves is the "±" shown. Changes smaller than that are only
   noise of that one map, so the advice says to keep the area (the numbers are still
   shown). If the notes of a map are all in one part of the screen they can't tell the
   width: then only the position is changed.

Presses far from any circle (more than two radii away) don't pull the area, and a map
given up halfway (pen lifted, or pressing anywhere until the end) counts only up to where
you stopped.

The advice assumes your hand makes the same movements with the new area. Change it, play
another map, and see what the next replay says.

## The Replay section

Play a map to the end in osu! or osu!lazer. When the replay is saved (osu! saves one for
every map you finish), the advice appears at once:

- what to change, in words: *Make it 1.20 mm wider, then move it 0.80 mm right and 0.30
  mm up*;
- the area you played with and the suggested one, number by number, as OpenTabletDriver
  writes them;
- the clicks outside the circle with your area and with the suggested one (same
  movements), where you press around the circles on average, and how sure it is;
- the two areas drawn on your tablet and all your presses drawn around a circle.

**Put this area in OpenTabletDriver** applies it; **Go back** returns to the area before.
Only the play just made counts: replays saved before you opened the program are left
out, unless you press **Analyse my latest replay**, which takes the newest replay there
is (useful when you open Area Coach after playing).

The area of each click is the one that was in use at that moment: Area Coach follows the
area in OpenTabletDriver while it is open, so any area you set, by hand or with a
button, counts for the clicks made with it.

This section only reads the replay file osu! saves after a map, and changes the area only
when you press the button: the same as setting it by hand in OpenTabletDriver.

## The Live section

![Live calibration](docs/screenshots/live.png)

Press **Start live calibration** and play. Area Coach:

- reads the map you are playing from the osu! window title (stable and lazer), and finds
  it in your songs;
- notes every press of the keys that click (the ones in osu!'s settings, for example V and
  B) with the cursor position, a thousand times a second;
- lines the presses up with the circles of the map, finding by itself the speed (DT,
  HT, and any speed in lazer), Hard Rock, pauses and retries.

### Stretches, not constant changes

It plays in **stretches** of about 150 clicks (200 with *gentle*, 100 with *strong*):

- **Time to get used to it.** After every change, the first clicks count less: nothing
  at first, then more and more, fully after 15 to 90 clicks depending on how big the
  change was. Getting used to a new area throws your presses off at any width, and
  that isn't held against the new width.
- **Changes between maps.** When a stretch is complete, the change comes at the end of
  the map, so you start the next one with the new area. In a long map, it comes in a
  pause, after a double stretch.
- **Small changes.** It moves the area part of the way towards the one your last clicks
  fit: with *normal*, at most 1.5% of the width and 0.8 mm per change. A sound plays at
  every change: lower when the area grows, higher when it shrinks, in between when it
  only moves.
- **Calm when you're fine.** If your clicks don't ask clearly for another width or
  position (the difference is within what the stretch can tell), nothing changes.

It starts from whatever area is set, and an area you set by hand while it runs is where
it goes on from. At the end of every map the area is saved in OpenTabletDriver's file;
**Back to that area** returns to the area you had when you started.

### Your widths: the band it finds by itself

Following your last clicks alone, Live could keep shrinking (or growing) the area: if you
land short on fast jumps, it shrinks it; and a smaller area may line up better with your
hand while you press further from the circles, because every bit of shake of the hand
becomes more pixels. So Live also remembers, for each millimetre of width you played:

- **how you land on jumps**: long or short, and by how much;
- **how far from the centre of the circles you press, compared on notes alike**: the
  same kind of jump with the same time for it, so that a hard map doesn't make a width
  look bad. A press far off counts as one and a half radii from the centre, no more:
  missing a lot on a hard map is normal and doesn't outweigh everything else. Every click says
  something this way, not only the misses, so a few maps are enough.

A width is judged only when there are at least about 60 clicks and three different maps
around it: one map can just be easy. From that it finds:

- **your band**: the widths that go about as well as the best one. Inside it, Live moves
  freely. Past its edges it goes only a little (about 1-1.5 mm), with half steps, slower
  still where you are seen to press further from the centres that way; if you really
  do, that edge becomes a wall and the area comes back. If it goes well out there, the band stretches:
  it is elastic, and follows you as your hand changes;
- **where you go from long to short**: if you land long below a width and short above
  it, it heads there instead of bouncing back and forth;
- **habits**: if you land short (or long) by about the same share at every width, the
  area isn't what causes it, and shrinking would only follow you: it stops chasing it
  and tells you so;
- **the width where you press closest to the centres**: the lowest point of a curve
  through the widths around it, so it moves smoothly as data comes instead of jumping
  from one millimetre to another. The surer it is, the more Live goes there, as long as
  it is clearly better than where you are (among widths about as good, it doesn't
  wander).

**Trying nearby widths.** Data only where you already play would leave little to compare.
So, while it isn't sure of the best width, now and then it plays one stretch at a nearby
width, about 2 mm narrower or wider: first on a side with no data yet (towards where your
clicks point first), then where the best might still be, and then comes back. It never does two in a
row, never past a wall, never outside your width range, and stops once it is sure. You
can turn it off in Settings.

### The best area and its reliability

**Best area so far** shows the area to keep when you've had enough of calibrating: the
width where you press closest to the centres (where several widths go about as well, the
one closest to where you play), at the place on the tablet where the area went best.
**Use it and stop Live** puts it in OpenTabletDriver and stops Live.

The **reliability** bar grows as you play. It is how sure it is that the best width is
really where you press closest to the centres: the distances of every width are drawn
again within their uncertainty, many times, and each time it looks at how much you would
lose by keeping the best width instead of the lowest point of the new curve. Widths about
as good as the best count as good (it doesn't matter which of them it picks), and one
width with few clicks moves the curve only a little, so the bar doesn't fall because the
best slides inside your band or because a width barely measured looks good by chance. It
also needs widths measured on both sides of the best (or of the band), and plenty of
clicks around it. From 75% the best area is reliable: you can stop calibrating and keep
it, and Live stops trying other widths.

If the bar goes down by 10 points or more, a note says from how much and why: a width
with fewer clicks now looks better than the old best, and Live is checking it. A few
stretches there settle it, one way or the other.

The memory is kept per tablet and per shape of area, across days: a day counts half
after 10 days and is forgotten after 60. The maps you finish with Live off count too
(through their replays, with the same care for the first clicks after a change). **Your
widths** shows it all, and **Forget it and start again** clears it.

**For practice: offline or unranked.** Live changes the area while you play, from how you
are playing: use it to practise, then play ranked with the best area it found and Live
off. It doesn't touch osu!: it reads the window title, the cursor and the keys that
click, like a key overlay, and changes the area in OpenTabletDriver.

## Settings

![Settings](docs/screenshots/settings.png)

- **Height / width**: the shape of the area, kept fixed (0.5625 is 16:9, 0.75 is 4:3).
  Empty: the shape of the area in use.
- **Width range**: optional. Live finds the widths that suit you by itself; a range here
  is a hard wall for it and for the advice.
- **Keys that click** (Live): empty takes them from osu! stable's settings (Z and X for
  lazer). **Pen tip clicks**: whether the pen tip (a mouse button) counts as a press.
- **Each change** (Live): gentle, normal or strong: how often (after about 200, 150 or
  100 clicks) and how far each change goes. **Try nearby widths**: whether it may play a
  stretch at a nearby width to measure it. **Sound**: on or off.
- **Where things are**: the osu! and osu!lazer folders, OpenTabletDriver's
  `settings.json` and the tablet, when they aren't found by themselves.

## What it reads and changes

- **Reads**: the replays osu! saves (`Data/r` for stable, the files folder for lazer),
  your maps, osu!'s settings (resolution, window, keys), OpenTabletDriver's settings.
  In Live also the osu! window title and position, the cursor position and which keys
  are held.
- **Changes**: only the area of your tablet profile in OpenTabletDriver, when you press
  a button or while Live runs. It sets it in the running OpenTabletDriver and in its
  `settings.json` (a copy of the original, `settings.json.areacoach-backup`, is kept the
  first time).
- Nothing is sent anywhere. Its own files (settings, the memory of your widths in
  `calibration.json`, a log) are in `%APPDATA%\AreaCoach`.

## Requirements and install

- Windows 10 or 11
- [OpenTabletDriver](https://opentabletdriver.net) 0.6, in absolute mode
- osu! stable or osu!lazer
- The Microsoft Edge WebView2 Runtime (part of Windows 11; without it the interface opens
  in the browser)

Download `AreaCoach.exe` from the Releases page and start it: there is nothing to
install.

## Troubleshooting

- **The Replay section keeps waiting.** osu! saves a replay only for maps you finish. If
  osu! is in an unusual folder, set it in Settings.
- **"I can't find the map"**: a map downloaded a moment ago is found once Area Coach has
  read the Songs folder again (a few seconds); in lazer, once its files are read.
- **Live stays on "Lining up"**: check *Keys that click* in Settings (the log shows the
  keys it reads). Relax, Autopilot and mods that mirror or move the circles can't be
  lined up.
- **When can I stop calibrating?** When the reliability bar passes 75%: then press *Use
  the best area* and keep it for a while; a fixed area lets your hand settle, which
  matters more than tenths of a millimetre.
- **OpenTabletDriver doesn't answer**: start it. Without it the area is still written in
  its file, and it takes it when it starts.
- **The OpenTabletDriver window shows old numbers**: press *Reopen the OpenTabletDriver
  window* after applying.

## Build from source

Requires Go 1.24 or newer.

```sh
# Windows exe (from any OS)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o AreaCoach.exe .
```

Add `-X areacoach/internal/server.Version=X.Y.Z` to `-ldflags` to stamp the version.
`rsrc_windows_amd64.syso` holds the Windows icon and manifest, built from `build/`:

```sh
go run github.com/akavel/rsrc@latest -manifest build/app.manifest -ico build/icon.ico -o rsrc_windows_amd64.syso
```

Run the tests with `go test ./...`: they include a simulated player for the Replay
section, one for Live (with a fake OpenTabletDriver), and players of different kinds for
the memory of the widths (one who presses further from the centres below a width, one
whose hand wants a smaller area, one who needs a bigger one, one who always lands short,
one who is off more after every change while getting used to it, and a check that the
reliability doesn't collapse once it is high), about a minute in all.

Pushing a tag like `v3.1.0` makes the *Release* workflow build `AreaCoach.exe` and attach
it, with its checksum, to the GitHub release.

### Layout

| Path | Contents |
| --- | --- |
| `internal/aim` | Judging the clicks of a play, and the search for the area |
| `internal/live` | Following a map while it is played: presses, lining up, sounds |
| `internal/calib` | The memory of your widths: the band, long and short, habits |
| `internal/app` | Replays, the area in OpenTabletDriver, the Live section, settings |
| `internal/geom` | From osu! pixels to the screen and the tablet |
| `internal/osu` | Readers for maps, replays (stable and lazer), `osu!.db`, the settings and osu!lazer's files |
| `internal/otd` | OpenTabletDriver settings and its connection |
| `internal/platform` | Windows: the osu! window, cursor, keys, sound |
| `internal/server`, `web` | The local interface |

## License and credits

[MIT](LICENSE).

Written by **Claude (Opus 5.5)**, Anthropic's AI model, under the direction of the
project author.

Not affiliated with osu!, ppy or OpenTabletDriver.
