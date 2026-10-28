// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Core in this file is a port of RadialFollowCore from AbstractOTDPlugins
// (https://github.com/AbstractQbit/AbstractOTDPlugins), by AbstractQbit.

// Package filter simulates OpenTabletDriver smoothing filters on the pen
// movements recorded in replays, and looks for the settings that would have
// put the most clicks inside the circles.
package filter

import "math"

// Params are the settings of AbstractQbit's Radial Follow Smoothing in
// tablet coordinates (radii in millimetres).
type Params struct {
	Outer float64 `json:"outer"`
	Inner float64 `json:"inner"`
	Coef  float64 `json:"coef"`
	Knee  float64 `json:"knee"`
	Leak  float64 `json:"leak"`
}

// Core is a faithful port of RadialFollowCore (AbstractOTDPlugins, MPL-2.0):
// the cursor follows the pen, never lagging more than the outer radius, with
// a deadzone of the inner radius and a smooth knee in between.
type Core struct {
	rOuter, rInner, coef, knee, leak float64
	xOffset, scaleComp               float64
	cx, cy                           float64
	started                          bool
}

// NewCore applies the same clamping as the plugin's property setters.
func NewCore(p Params) *Core {
	c := &Core{
		rOuter: clamp(p.Outer, 0, 1e6),
		rInner: clamp(p.Inner, 0, 1e6),
		coef:   clamp(p.Coef, 0.0001, 1),
		knee:   clamp(p.Knee, 0, 100),
		leak:   clamp(p.Leak, 0, 1),
	}
	if c.knee > 0.0001 {
		c.xOffset = c.inverseKneeScaled(0)
		c.scaleComp = c.derivKneeScaled(c.xOffset)
	} else {
		c.xOffset, c.scaleComp = -1, 1
	}
	return c
}

// Reset puts the cursor on the pen (the plugin does this on pen detection).
func (c *Core) Reset(x, y float64) { c.cx, c.cy, c.started = x, y, true }

// Step processes one tablet report and returns the cursor.
func (c *Core) Step(x, y float64) (float64, float64) {
	if !c.started {
		c.Reset(x, y)
		return x, y
	}
	dx, dy := x-c.cx, y-c.cy
	dist := math.Hypot(dx, dy)
	move := c.deltaFn(dist)
	if dist > 0 {
		c.cx += dx / dist * move
		c.cy += dy / dist * move
	}
	if math.IsNaN(c.cx) || math.IsNaN(c.cy) || math.IsInf(c.cx, 0) || math.IsInf(c.cy, 0) {
		c.cx, c.cy = x, y
	}
	return c.cx, c.cy
}

func (c *Core) kneeFunc(x float64) float64 {
	switch {
	case x < -3:
		return x
	case x < 3:
		return math.Log(math.Tanh(math.Exp(x)))
	}
	return 0
}

func (c *Core) kneeScaled(x float64) float64 {
	if c.knee > 0.0001 {
		return c.knee*c.kneeFunc(x/c.knee) + 1
	}
	if x > 0 {
		return 1
	}
	return 1 + x
}

func (c *Core) inverseKneeScaled(x float64) float64 {
	it := func(v float64) float64 { return math.Log((1+v)/(1-v)) / 2 }
	return c.knee * math.Log(it(math.Exp((x-1)/c.knee)))
}

func (c *Core) derivKneeScaled(x float64) float64 {
	e := math.Exp(x / c.knee)
	t := math.Tanh(e)
	return (e - e*(t*t)) / t
}

func (c *Core) rOuterAdj() float64 { return math.Max(c.rOuter, c.rInner+0.0001) }

func (c *Core) leakedFn(x float64) float64 {
	return c.kneeScaled(x+c.xOffset)*(1-c.leak) + x*c.leak*c.scaleComp
}

func (c *Core) smoothedFn(x float64) float64 { return c.leakedFn(x * c.coef / c.scaleComp) }

func (c *Core) scaleToOuter(x float64) float64 {
	span := c.rOuterAdj() - c.rInner
	return span * c.smoothedFn(x/span)
}

func (c *Core) deltaFn(x float64) float64 {
	if x > c.rInner {
		return x - c.scaleToOuter(x-c.rInner) - c.rInner
	}
	return 0
}

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }
