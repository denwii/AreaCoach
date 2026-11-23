# Changelog

## 3.1.0

- **Live finds your range by itself.** It remembers how you play at each width, day
  after day: how you land on jumps (long or short) and your misses compared on notes
  alike. From that it finds the band of widths that suit you: inside it moves freely,
  past the edges only a little and with half steps, and where you clearly miss more an
  edge becomes a wall and the area comes back. The band is elastic: it stretches when a
  new width goes well, and follows you as your hand changes.
- **No more chasing a habit.** If you land short (or long) by the same share at every
  width, it stops shrinking (or growing) for it and says so. If you land long below a
  width and short above it, it heads there instead of bouncing.
- **Rough first, then finer**: the changes get smaller as it learns; widths not measured
  yet are crossed with smaller steps.
- A width is judged only with clicks from several maps around it: one easy map doesn't
  move the band.
- **Your widths** in the Live section shows the band and every width; **Forget it and
  start again** clears it. Each change says why it was made.
- The maps you finish with Live off count too, through their replays. The Replay advice
  still comes from that play alone, and says when it falls outside your band.
- The width range in Settings is now optional.

## 3.0.0

Rewritten from scratch, simpler.

- **Two sections.** *Replay*: as soon as a map is finished, the advice from that replay
  alone, with a button to put it in OpenTabletDriver. *Live*: for practice, the area is
  reshaped a little at a time while you play, with a sound at every change.
- **Plain geometry.** The advice is the area (with your width range and your fixed height
  / width ratio) with which the same pen movements put the most clicks inside the
  circles, and among those the closest to their centres. No consistency model, no
  history: only the play just made counts.
- **How sure** each advice is, from the play itself; a map whose notes can't tell the
  width only moves the area.
- **Live** reads the map from the osu! window title (stable and lazer), the cursor and the
  keys that click, lines the presses up with the circles by itself (speed, Hard Rock,
  pauses, retries) and every 30 clicks moves the area part of the way to the one that
  fits the last 150. Any area set by hand is where it starts from.
- Removed: the diagnosis over many maps, ratings, the filter tuning, the plays and
  history pages. The folders, the tablet, the width range and the fixed shape of version
  2 are kept.

## 2.7.2

- **The suggestion no longer changes by itself.** With the program open and no maps
  played, it could change after a while: right after a start the replays were still
  being analysed and read (a part at a time), and the suggestion followed every step.
  Now it uses your plays only once all of them are read, takes the new analysis as soon
  as it's ready, and until then shows the area found before, marked *not final yet*, with
  the button to put it in OpenTabletDriver waiting. After that it changes only with the
  maps you play or what you tell it. Every change is written in the log with its reason.
- When both osu! stable and osu!lazer are installed, the game whose window is used is the
  one of your own latest play (a replay of another player watched in lazer no longer
  counts).

## 2.7.1

- **The suggested width follows your misses.** After every map the aim misses you made
  are compared with those the model expected on the same notes (a map with high CS
  expects more): when some widths miss fewer than expected and others more, the
  suggestion leans that way, as far as the maps are sure of it. The line *Your real
  misses* shows it. The maps of earlier diagnoses count too.
- **It moves even by a little**: the width found no longer stays put unless another
  promises 3% fewer misses; it goes to the width with the fewest expected misses, to a
  tenth of a millimetre, and stays only against differences under half a percent.
- Misses always in the same spot no longer keep the area from growing, when a bigger one
  really makes you miss fewer there (the area still doesn't move toward them).

## 2.7.0

- **osu!lazer.** Its replays and maps are read from its files folder (found by what they
  contain; also when its data was moved), with its window settings and screen scaling.
  The area and the filter stay in OpenTabletDriver, running on its own as with stable.
  DT and HT at any speed and Difficulty Adjust are taken into account; lazer mods that
  move the circles or change the speed during the map are left out, and so are the
  replays of other players you watched. Stable and lazer can be used together.
- **All your replays count, however old**: every replay is imported (in the background,
  the newest first), the analysis and the diagnosis use all your plays (the older ones
  with part of their notes, so it stays fast). Those from before the first start count as
  played with the area you had then. Before, only up to 300 from the 60 days before the
  first start, and the latest 1000 plays.
- **How good the area already is**: a percentage of how close the suggested area is to
  the ideal one, in aim misses. From 95% it's good: with the ideal area you'd miss at most
  5% fewer circles. Below, it says about how many maps more it needs. The first
  suggestion comes straight from your replays.
- **Your lean is yours**: if your clicks stay off to one side wherever the area is, it's
  a habit of your aim. The place of the area no longer goes after it, and goes back to
  where your hand is at home. The page says so (*Your lean*).
- **Maps given up** (pen lifted, no clicks or clicks anywhere until the game is over):
  the notes from where you stopped playing are left out, also in the replays already
  imported.
- **The filter is now Devocub Antichatter** (TabletDriverFilters) instead of Radial
  Follow. It looks for the least latency that keeps the aim straight and snappy, and
  measures the **misses from moving too fast** (on the circle a moment before the click,
  off it at the click: overshoots): the suggestion is the lightest setting that gives
  nearly all of the gain. The Filter page and the guided filter show those misses and
  how straight the paths get.

## 2.6.0

- **The diagnosis suggests, you decide.** It never changes the area by itself any more:
  after each map it shows the suggested area, what to change to get there and the numbers
  to type; **Put it in OpenTabletDriver** does it when you want. Play any area you like
  to test: every map is measured with the area it was played with, and it never pauses
  for it. The Testing switch, the tries and "Keep the area if no aim miss" are gone.
- **All your replays count**: up to the latest 1000 of the profile besides the diagnosis
  maps (before it, from earlier diagnoses, played while paused or with the program
  closed), each with its area. An area is suggested as soon as the diagnosis starts, and
  Start over keeps everything: replays, ratings, adjustments and the area found.
- **Feedback buttons**: Bigger, Smaller, Left, Right, Up, Down move the suggested area
  (2% / 1 mm a click) and are kept from then on.
- **Relax**: replays played with Relax are used (osu! clicks, the aim is the player's).
  The ones skipped by earlier versions are looked at again once.
- The area the maps are measured with is the one OpenTabletDriver really uses, also when
  it was only applied and not saved.
- A diagnosis running is no longer paused when the program closes.

## 2.5.0

- **Width / height range** for the Free shape (for example `1.4 - 1.6`), like the widths
  range: every area stays within it; inside, the jumps decide, and without a clear sign
  the ratio stays in the middle. The page says when the area found is at a limit and the
  ratio it would have without it.
- **Areas you set yourself** in OpenTabletDriver during the diagnosis no longer pause it:
  the next map is measured with your area like any other, then it carries on with its
  own. The old numbers the OpenTabletDriver window still shows are told apart more
  precisely (the area it had when the window was opened).
- **Your past replays count in the diagnosis:** the normal plays before it (up to the
  latest 300), each with the area it was played with, with the older ones counting less.
  The first maps of a diagnosis land much closer.
- **Replays from before the first start:** up to 300 from the 60 days before are used,
  counted as played with the area you had at the first start (imported once, quickly).
- **Rate each area from 1 to 10** after the map: among widths with about the same
  expected aim misses the diagnosis prefers the ones you rate higher and moves away from
  the ones you rate low. Ratings are shown in the maps list.

## 2.4.0

- New **Testing** switch in the diagnosis (and on its start screen). Off, the new default:
  every map is played with the best area found so far, with no tries; it still changes map
  by map as the replays change what's best (fewest expected aim misses, from your
  consistency, how long or short you land and where you're steadiest). On: every map tries
  a slightly different area, as before, to learn faster. Switching it off puts the best
  area in straight away.

## 2.3.3

- Replays are no longer filtered by player name: replays saved while not logged in
  (osu! leaves the name empty) or with a different name in the osu! config are used too.
  The "Player name to analyse" setting is gone.
- The OpenTabletDriver window can stay open: when it saves an old area (Save with the old
  numbers), the diagnosis area is put back in the file too, instead of pausing. The notice
  about the window is clearer, with a "Show the current area in OpenTabletDriver" button.
- Once the diagnosis is sure enough, the width tries vary in size, so the widths in
  between are played too instead of always the same two areas.

## 2.3.2

- Works with any area rotation in OpenTabletDriver (90°, 180°, 270° or any other angle):
  the first area is the largest that fits the tablet turned that way, every area stays
  inside the tablet, the side tries and the turned-aim correction follow the area's own
  width and height, and directions in the interface are as seen on the screen.

## 2.3.1

- Only the replays saved since you first started Area Coach are used: your old replays
  are no longer imported (with a big replay folder that import could take very long).
  Plays from before the first start are removed from the analysis.

## 2.3.0 — first public release

- Interface, messages and log in English.
- In-game diagnosis, map by map: the area for the next map is set in OpenTabletDriver
  when osu! saves the replay; every map tries a slightly different width and, every two
  maps, a slightly different place, to find where you are most precise and steadiest.
- Guided Radial Follow filter: the program tells you the values for the next map, you set
  them in OpenTabletDriver, and after every replay it gives you new ones. Choice between
  the lowest latency and more smoothing.
- Area and filter "perfection" percentages.
- Replay import: failed and multiplayer plays are picked up when saved with F2; the Plays
  page lists the replays found but not used, with the reason, and the folders it watches.
- Grip profiles, area history, replay analysis pages (Area, Progress, Plays, Filter).
