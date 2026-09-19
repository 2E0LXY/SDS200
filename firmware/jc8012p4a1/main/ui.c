/*
 * SDS200 Panel user interface: dark landscape theme with large touch targets.
 *
 * All widgets are created and updated in the LVGL task. Other tasks never
 * touch LVGL; a 100 ms timer pulls snapshots from the scanner, audio and
 * network modules and refreshes whatever changed.
 */
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include "esp_log.h"
#include "esp_app_desc.h"
#include "esp_heap_caps.h"
#include "esp_idf_version.h"
#include "lwip/inet.h"
#include "app_config.h"
#include "settings.h"
#include "board.h"
#include "net.h"
#include "scanner.h"
#include "audio.h"
#include "app.h"
#include "ui.h"

static const char *TAG = "ui";

/* ----------------------------------------------------------------- style -- */

#define COL_BG        0x0B0F14
#define COL_PANEL     0x151B23
#define COL_PANEL2    0x1E2630
#define COL_TEXT      0xE6EDF3
#define COL_MUTED     0x8B98A5
#define COL_ACCENT    0x2F81F7
#define COL_GOOD      0x2EA043
#define COL_WARN      0xD29922
#define COL_BAD       0xDA3633
#define COL_AMBER     0xFFB000

#define NAV_W         176
#define BAR_H         56
#define CONTENT_W     (UI_H_RES - NAV_W)
#define CONTENT_H     (UI_V_RES - BAR_H)

enum { PG_LIVE, PG_REMOTE, PG_FQK, PG_WF, PG_AUDIO, PG_SETTINGS, PAGE_COUNT };
static const char *const s_page_names[PAGE_COUNT] = {
    LV_SYMBOL_HOME " Live", LV_SYMBOL_KEYBOARD " Remote", LV_SYMBOL_LIST " Quick Keys",
    LV_SYMBOL_IMAGE " Waterfall", LV_SYMBOL_AUDIO " Audio", LV_SYMBOL_SETTINGS " Settings",
};

static lv_obj_t *s_pages[PAGE_COUNT];
static lv_obj_t *s_nav[PAGE_COUNT];
static int s_page = -1;

static scanner_state_t *s_sc;          /* latest snapshot (PSRAM) */
static uint32_t s_seen_gsi = UINT32_MAX, s_seen_sts = UINT32_MAX, s_seen_fqk = UINT32_MAX;
static uint32_t s_seen_wf = UINT32_MAX, s_seen_disc = 0;

/* status bar */
static lv_obj_t *s_bar_scanner, *s_bar_wifi, *s_bar_audio, *s_bar_msg;

/* live */
static lv_obj_t *s_lv_mode, *s_lv_channel, *s_lv_freq;
static lv_obj_t *s_lv_rows[7];
static lv_obj_t *s_lv_hold_sys, *s_lv_hold_dept, *s_lv_hold_chan;
static lv_obj_t *s_lv_bars[5], *s_lv_rssi;
static lv_obj_t *s_vol_slider, *s_vol_label, *s_sql_slider, *s_sql_label;
static uint32_t s_vol_touch_ms, s_sql_touch_ms;

/* remote */
static lv_obj_t *s_mirror;
static lv_obj_t *s_mirror_note;

/* FQK */
static lv_obj_t *s_fqk_btn[FQK_COUNT];
static lv_obj_t *s_fqk_status;
static uint8_t s_fqk_local[FQK_COUNT];

/* waterfall */
static lv_obj_t *s_wf_canvas, *s_wf_chart, *s_wf_status, *s_wf_btn_label;
static lv_chart_series_t *s_wf_series;
static uint16_t *s_wf_buf;
static uint16_t s_wf_lut[256];
static float s_wf_lo = 0, s_wf_hi = 255;
#define WF_PX     4
#define WF_W      (WF_BINS * WF_PX)
#define WF_H      420

/* audio */
static lv_obj_t *s_au_btn, *s_au_btn_label, *s_au_state, *s_au_stats, *s_au_vol, *s_au_vol_label;

/* settings */
static lv_obj_t *s_set_wifi, *s_set_ip_ta, *s_set_ip_status, *s_set_bl, *s_set_rev, *s_set_flip;
static lv_obj_t *s_kb;                 /* shared keyboard for settings */

/* Wi-Fi overlay */
static lv_obj_t *s_wifi_ov, *s_wifi_list, *s_wifi_ssid, *s_wifi_pass, *s_wifi_kb, *s_wifi_status;
static uint32_t s_wifi_scan_seen;

static uint32_t tick_ms(void) { return lv_tick_get(); }

static lv_obj_t *mk_label(lv_obj_t *parent, const lv_font_t *font, uint32_t color, const char *txt)
{
    lv_obj_t *l = lv_label_create(parent);
    lv_obj_set_style_text_font(l, font, 0);
    lv_obj_set_style_text_color(l, lv_color_hex(color), 0);
    lv_label_set_text(l, txt);
    return l;
}

static lv_obj_t *mk_panel(lv_obj_t *parent, int x, int y, int w, int h)
{
    lv_obj_t *p = lv_obj_create(parent);
    lv_obj_set_pos(p, x, y);
    lv_obj_set_size(p, w, h);
    lv_obj_set_style_bg_color(p, lv_color_hex(COL_PANEL), 0);
    lv_obj_set_style_border_width(p, 0, 0);
    lv_obj_set_style_radius(p, 12, 0);
    lv_obj_set_style_pad_all(p, 14, 0);
    lv_obj_remove_flag(p, LV_OBJ_FLAG_SCROLLABLE);
    return p;
}

static lv_obj_t *mk_button(lv_obj_t *parent, const char *txt, int w, int h, uint32_t color,
                           lv_event_cb_t cb, void *user)
{
    lv_obj_t *b = lv_button_create(parent);
    lv_obj_set_size(b, w, h);
    lv_obj_set_style_bg_color(b, lv_color_hex(color), 0);
    lv_obj_set_style_radius(b, 10, 0);
    lv_obj_set_style_shadow_width(b, 0, 0);
    lv_obj_t *l = lv_label_create(b);
    lv_label_set_text(l, txt);
    lv_obj_set_style_text_font(l, &lv_font_montserrat_24, 0);
    lv_obj_center(l);
    if (cb) {
        lv_obj_add_event_cb(b, cb, LV_EVENT_CLICKED, user);
    }
    return b;
}

static void flash_msg(const char *msg)
{
    lv_label_set_text(s_bar_msg, msg);
}

/* ------------------------------------------------------------ navigation -- */

static void show_page(int pg)
{
    if (pg == s_page) {
        return;
    }
    for (int i = 0; i < PAGE_COUNT; i++) {
        if (i == pg) {
            lv_obj_remove_flag(s_pages[i], LV_OBJ_FLAG_HIDDEN);
            lv_obj_set_style_bg_color(s_nav[i], lv_color_hex(COL_ACCENT), 0);
        } else {
            lv_obj_add_flag(s_pages[i], LV_OBJ_FLAG_HIDDEN);
            lv_obj_set_style_bg_color(s_nav[i], lv_color_hex(COL_PANEL2), 0);
        }
    }
    if (s_kb) {
        lv_obj_add_flag(s_kb, LV_OBJ_FLAG_HIDDEN);
    }
    s_page = pg;
    if (pg == PG_FQK) {
        scanner_fqk_refresh();
    }
}

static void nav_cb(lv_event_t *e)
{
    show_page((int)(intptr_t)lv_event_get_user_data(e));
}

static void key_cb(lv_event_t *e)
{
    char code = (char)(intptr_t)lv_event_get_user_data(e);
    if (!scanner_key(code)) {
        flash_msg("Key queue full");
    }
}

/* ------------------------------------------------------------ live page -- */

static const char *const s_row_names[7] = {
    "System", "Department", "Site", "TGID", "Unit ID", "Modulation", "Service",
};

static void vol_cb(lv_event_t *e)
{
    lv_event_code_t code = lv_event_get_code(e);
    int v = lv_slider_get_value(s_vol_slider);
    lv_label_set_text_fmt(s_vol_label, "Volume  %d", v);
    s_vol_touch_ms = tick_ms();
    if (code == LV_EVENT_RELEASED) {
        scanner_set_volume(v);
    }
}

static void sql_cb(lv_event_t *e)
{
    lv_event_code_t code = lv_event_get_code(e);
    int v = lv_slider_get_value(s_sql_slider);
    lv_label_set_text_fmt(s_sql_label, "Squelch  %d", v);
    s_sql_touch_ms = tick_ms();
    if (code == LV_EVENT_RELEASED) {
        scanner_set_squelch(v);
    }
}

static lv_obj_t *mk_hold_tag(lv_obj_t *parent)
{
    lv_obj_t *t = mk_label(parent, &lv_font_montserrat_16, 0x000000, "HOLD");
    lv_obj_set_style_bg_color(t, lv_color_hex(COL_AMBER), 0);
    lv_obj_set_style_bg_opa(t, LV_OPA_COVER, 0);
    lv_obj_set_style_pad_hor(t, 8, 0);
    lv_obj_set_style_pad_ver(t, 2, 0);
    lv_obj_set_style_radius(t, 6, 0);
    lv_obj_add_flag(t, LV_OBJ_FLAG_HIDDEN);
    return t;
}

static void build_live(lv_obj_t *pg)
{
    lv_obj_t *card = mk_panel(pg, 0, 0, 700, 600);
    s_lv_mode = mk_label(card, &lv_font_montserrat_20, COL_MUTED, "Waiting for scanner...");
    lv_obj_set_pos(s_lv_mode, 0, 0);

    s_lv_channel = mk_label(card, &lv_font_montserrat_40, COL_TEXT, "-");
    lv_obj_set_pos(s_lv_channel, 0, 34);
    lv_obj_set_width(s_lv_channel, 560);
    lv_label_set_long_mode(s_lv_channel, LV_LABEL_LONG_DOT);
    s_lv_hold_chan = mk_hold_tag(card);
    lv_obj_set_pos(s_lv_hold_chan, 580, 46);

    s_lv_freq = mk_label(card, &lv_font_montserrat_40, COL_ACCENT, "");
    lv_obj_set_pos(s_lv_freq, 0, 90);

    for (int i = 0; i < 7; i++) {
        lv_obj_t *n = mk_label(card, &lv_font_montserrat_20, COL_MUTED, s_row_names[i]);
        lv_obj_set_pos(n, 0, 160 + i * 56);
        s_lv_rows[i] = mk_label(card, &lv_font_montserrat_28, COL_TEXT, "");
        lv_obj_set_pos(s_lv_rows[i], 160, 154 + i * 56);
        lv_obj_set_width(s_lv_rows[i], 400);
        lv_label_set_long_mode(s_lv_rows[i], LV_LABEL_LONG_DOT);
    }
    s_lv_hold_sys = mk_hold_tag(card);
    lv_obj_set_pos(s_lv_hold_sys, 580, 162);
    s_lv_hold_dept = mk_hold_tag(card);
    lv_obj_set_pos(s_lv_hold_dept, 580, 218);

    /* signal + levels */
    lv_obj_t *side = mk_panel(pg, 716, 0, CONTENT_W - 716 - 16, 600);
    mk_label(side, &lv_font_montserrat_20, COL_MUTED, "Signal");
    for (int i = 0; i < 5; i++) {
        s_lv_bars[i] = lv_obj_create(side);
        lv_obj_set_size(s_lv_bars[i], 34, 20 + i * 16);
        lv_obj_set_pos(s_lv_bars[i], i * 44, 120 - (20 + i * 16));
        lv_obj_set_style_radius(s_lv_bars[i], 4, 0);
        lv_obj_set_style_border_width(s_lv_bars[i], 0, 0);
        lv_obj_set_style_bg_color(s_lv_bars[i], lv_color_hex(COL_PANEL2), 0);
        lv_obj_remove_flag(s_lv_bars[i], LV_OBJ_FLAG_SCROLLABLE | LV_OBJ_FLAG_CLICKABLE);
    }
    s_lv_rssi = mk_label(side, &lv_font_montserrat_24, COL_TEXT, "-- dBm");
    lv_obj_set_pos(s_lv_rssi, 240, 84);

    s_vol_label = mk_label(side, &lv_font_montserrat_24, COL_TEXT, "Volume  --");
    lv_obj_set_pos(s_vol_label, 0, 170);
    s_vol_slider = lv_slider_create(side);
    lv_slider_set_range(s_vol_slider, 0, 29);
    lv_obj_set_size(s_vol_slider, 330, 36);
    lv_obj_set_pos(s_vol_slider, 20, 220);
    lv_obj_set_ext_click_area(s_vol_slider, 24);
    lv_obj_add_event_cb(s_vol_slider, vol_cb, LV_EVENT_VALUE_CHANGED, NULL);
    lv_obj_add_event_cb(s_vol_slider, vol_cb, LV_EVENT_RELEASED, NULL);

    s_sql_label = mk_label(side, &lv_font_montserrat_24, COL_TEXT, "Squelch  --");
    lv_obj_set_pos(s_sql_label, 0, 300);
    s_sql_slider = lv_slider_create(side);
    lv_slider_set_range(s_sql_slider, 0, 19);
    lv_obj_set_size(s_sql_slider, 330, 36);
    lv_obj_set_pos(s_sql_slider, 20, 350);
    lv_obj_set_ext_click_area(s_sql_slider, 24);
    lv_obj_add_event_cb(s_sql_slider, sql_cb, LV_EVENT_VALUE_CHANGED, NULL);
    lv_obj_add_event_cb(s_sql_slider, sql_cb, LV_EVENT_RELEASED, NULL);

    /* action row */
    static const struct { const char *label; char code; uint32_t col; } acts[] = {
        {"Sys Hold", 'A', COL_PANEL2}, {"Dept Hold", 'B', COL_PANEL2}, {"Hold", 'C', COL_WARN},
        {"Avoid", 'L', COL_BAD}, {LV_SYMBOL_LEFT " Prev", '<', COL_PANEL2}, {"Next " LV_SYMBOL_RIGHT, '>', COL_PANEL2},
    };
    int bw = (CONTENT_W - 16 - 5 * 12) / 6;
    for (int i = 0; i < 6; i++) {
        lv_obj_t *b = mk_button(pg, acts[i].label, bw, 110, acts[i].col, key_cb, (void *)(intptr_t)acts[i].code);
        lv_obj_set_pos(b, i * (bw + 12), 616);
    }
}

static bool is_hold(const char *h)
{
    return h[0] && strcasecmp(h, "Off") != 0 && strcasecmp(h, "False") != 0;
}

static void set_hidden(lv_obj_t *o, bool hidden)
{
    if (hidden) {
        lv_obj_add_flag(o, LV_OBJ_FLAG_HIDDEN);
    } else {
        lv_obj_remove_flag(o, LV_OBJ_FLAG_HIDDEN);
    }
}

static void update_live(const scanner_state_t *s)
{
    char buf[96];
    snprintf(buf, sizeof(buf), "%s%s%s", s->mode[0] ? s->mode : "-", s->v_screen[0] ? "  /  " : "", s->v_screen);
    lv_label_set_text(s_lv_mode, buf);
    lv_label_set_text(s_lv_channel, s->channel[0] ? s->channel : "-");
    lv_label_set_text(s_lv_freq, s->frequency);
    const char *vals[7] = {s->system, s->department, s->site, s->tgid, s->unit_id, s->modulation, s->service_type};
    for (int i = 0; i < 7; i++) {
        lv_label_set_text(s_lv_rows[i], vals[i]);
    }
    set_hidden(s_lv_hold_sys, !is_hold(s->system_hold));
    set_hidden(s_lv_hold_dept, !is_hold(s->department_hold));
    set_hidden(s_lv_hold_chan, !is_hold(s->channel_hold));

    int sig = s->signal < 0 ? 0 : s->signal;
    for (int i = 0; i < 5; i++) {
        uint32_t c = i < sig ? (sig >= 4 ? COL_GOOD : (sig >= 2 ? COL_WARN : COL_BAD)) : COL_PANEL2;
        lv_obj_set_style_bg_color(s_lv_bars[i], lv_color_hex(c), 0);
    }
    if (s->rssi) {
        lv_label_set_text_fmt(s_lv_rssi, "%d dBm", s->rssi);
    } else {
        lv_label_set_text(s_lv_rssi, "-- dBm");
    }
    uint32_t now = tick_ms();
    if (s->volume >= 0 && !lv_obj_has_state(s_vol_slider, LV_STATE_PRESSED) && now - s_vol_touch_ms > 2000) {
        lv_slider_set_value(s_vol_slider, s->volume, LV_ANIM_OFF);
        lv_label_set_text_fmt(s_vol_label, "Volume  %d", s->volume);
    }
    if (s->squelch >= 0 && !lv_obj_has_state(s_sql_slider, LV_STATE_PRESSED) && now - s_sql_touch_ms > 2000) {
        lv_slider_set_value(s_sql_slider, s->squelch, LV_ANIM_OFF);
        lv_label_set_text_fmt(s_sql_label, "Squelch  %d", s->squelch);
    }
}

/* ----------------------------------------------------- screen mirror -- */

#define MIRROR_W   700
#define MIRROR_H   420

static void mirror_draw_cb(lv_event_t *e)
{
    lv_obj_t *obj = lv_event_get_target_obj(e);
    lv_layer_t *layer = lv_event_get_layer(e);
    lv_area_t a;
    lv_obj_get_content_coords(obj, &a);
    const scanner_state_t *s = s_sc;
    int n = s->sts_count;
    if (n <= 0) {
        return;
    }
    /* row heights: large rows count double */
    int units = 0;
    for (int i = 0; i < n; i++) {
        units += s->sts[i].large ? 2 : 1;
    }
    int h = lv_area_get_height(&a);
    int unit_h = h / (units ? units : 1);
    if (unit_h > 40) {
        unit_h = 40;
    }
    int cell_w = lv_area_get_width(&a) / STS_COLS;
    const lv_font_t *small = unit_h >= 30 ? &lv_font_montserrat_20 : &lv_font_montserrat_16;
    const lv_font_t *large = unit_h >= 30 ? &lv_font_montserrat_32 : &lv_font_montserrat_24;

    lv_draw_rect_dsc_t rd;
    lv_draw_rect_dsc_init(&rd);
    rd.bg_color = lv_color_hex(COL_AMBER);
    rd.radius = 0;
    lv_draw_label_dsc_t ld;
    lv_draw_label_dsc_init(&ld);
    ld.align = LV_TEXT_ALIGN_CENTER;
    ld.text_local = 1;
    lv_draw_line_dsc_t ln;
    lv_draw_line_dsc_init(&ln);
    ln.color = lv_color_hex(COL_AMBER);
    ln.width = 2;

    int y = a.y1;
    for (int i = 0; i < n; i++) {
        const sts_line_t *l = &s->sts[i];
        int rh = unit_h * (l->large ? 2 : 1);
        ld.font = l->large ? large : small;
        int fh = lv_font_get_line_height(ld.font);
        for (int c = 0; c < STS_COLS; c++) {
            char ch = l->text[c];
            char m = l->mode[c];
            lv_area_t cell = {
                .x1 = a.x1 + c * cell_w, .x2 = a.x1 + (c + 1) * cell_w - 1,
                .y1 = y, .y2 = y + rh - 1,
            };
            bool rev = (m == '*');
            if (rev) {
                lv_draw_rect(layer, &rd, &cell);
            }
            if (ch != ' ') {
                char t[2] = {ch, 0};
                ld.text = t;
                ld.color = lv_color_hex(rev ? 0x000000 : COL_AMBER);
                lv_area_t ta = cell;
                ta.x1 -= 6;          /* allow wide glyphs to overhang */
                ta.x2 += 6;
                ta.y1 = y + (rh - fh) / 2;
                ta.y2 = ta.y1 + fh;
                lv_draw_label(layer, &ld, &ta);
            }
            if (m == '_') {
                ln.p1.x = cell.x1;
                ln.p1.y = y + rh - 3;
                ln.p2.x = cell.x2;
                ln.p2.y = y + rh - 3;
                lv_draw_line(layer, &ln);
            }
        }
        y += rh;
    }
}

static void build_remote(lv_obj_t *pg)
{
    lv_obj_t *frame = mk_panel(pg, 0, 0, MIRROR_W + 28, MIRROR_H + 28);
    lv_obj_set_style_bg_color(frame, lv_color_hex(0x000000), 0);
    lv_obj_set_style_border_color(frame, lv_color_hex(COL_PANEL2), 0);
    lv_obj_set_style_border_width(frame, 2, 0);
    s_mirror = lv_obj_create(frame);
    lv_obj_remove_style_all(s_mirror);
    lv_obj_set_size(s_mirror, MIRROR_W, MIRROR_H);
    lv_obj_center(s_mirror);
    lv_obj_remove_flag(s_mirror, LV_OBJ_FLAG_CLICKABLE | LV_OBJ_FLAG_SCROLLABLE);
    lv_obj_add_event_cb(s_mirror, mirror_draw_cb, LV_EVENT_DRAW_MAIN, NULL);
    s_mirror_note = mk_label(frame, &lv_font_montserrat_24, COL_MUTED, "No display data yet");
    lv_obj_center(s_mirror_note);

    /* function keys below the mirror */
    static const struct { const char *label; char code; uint32_t col; } fk[] = {
        {"MENU", 'M', COL_PANEL2}, {"FUNC", 'F', COL_ACCENT}, {"REPLAY", 'Y', COL_PANEL2},
        {"AVOID", 'L', COL_BAD}, {"SERVICE", 'T', COL_PANEL2}, {"RANGE", 'R', COL_PANEL2},
        {"A", 'A', COL_PANEL2}, {"B", 'B', COL_PANEL2}, {"C", 'C', COL_PANEL2},
        {"ZIP", 'Z', COL_PANEL2}, {"VOL push", 'V', COL_PANEL2}, {"SQL push", 'Q', COL_PANEL2},
        {LV_SYMBOL_LEFT " L", '<', COL_PANEL2}, {"PUSH", '^', COL_PANEL2}, {"R " LV_SYMBOL_RIGHT, '>', COL_PANEL2},
    };
    int bw = (MIRROR_W + 28 - 4 * 10) / 5;
    for (size_t i = 0; i < sizeof(fk) / sizeof(fk[0]); i++) {
        lv_obj_t *b = mk_button(pg, fk[i].label, bw, 86, fk[i].col, key_cb, (void *)(intptr_t)fk[i].code);
        lv_obj_set_pos(b, (int)(i % 5) * (bw + 10), MIRROR_H + 40 + (int)(i / 5) * 96);
    }

    /* numeric keypad */
    static const struct { const char *label; char code; } kp[] = {
        {"1", '1'}, {"2", '2'}, {"3", '3'}, {"4", '4'}, {"5", '5'}, {"6", '6'},
        {"7", '7'}, {"8", '8'}, {"9", '9'}, {". / NO", '.'}, {"0", '0'}, {"E / YES", 'E'},
    };
    int kx = MIRROR_W + 28 + 20;
    int kw = (CONTENT_W - 16 - kx - 2 * 10) / 3;
    for (int i = 0; i < 12; i++) {
        uint32_t col = kp[i].code == 'E' ? COL_GOOD : COL_PANEL2;
        lv_obj_t *b = mk_button(pg, kp[i].label, kw, 150, col, key_cb, (void *)(intptr_t)kp[i].code);
        lv_obj_set_pos(b, kx + (i % 3) * (kw + 10), (i / 3) * 160);
        lv_obj_t *l = lv_obj_get_child(b, 0);
        if (kp[i].label[1] == 0) {
            lv_obj_set_style_text_font(l, &lv_font_montserrat_40, 0);
        }
    }
    lv_obj_t *hint = mk_label(pg, &lv_font_montserrat_16, COL_MUTED,
                              "Display refreshes about three times a second and after every key.");
    lv_obj_set_pos(hint, kx, 4 * 160 + 10);
}

/* -------------------------------------------------------------- FQK page -- */

static void fqk_paint(int i)
{
    static const uint32_t cols[3] = {0x20262E, 0x6E2B2B, 0x2EA043};
    lv_obj_set_style_bg_color(s_fqk_btn[i], lv_color_hex(cols[s_fqk_local[i] > 2 ? 0 : s_fqk_local[i]]), 0);
    lv_obj_set_style_text_color(lv_obj_get_child(s_fqk_btn[i], 0),
                                lv_color_hex(s_fqk_local[i] ? COL_TEXT : 0x55606B), 0);
}

static void fqk_cb(lv_event_t *e)
{
    int i = (int)(intptr_t)lv_event_get_user_data(e);
    if (!s_sc->fqk_valid) {
        flash_msg("Quick key states not loaded yet");
        return;
    }
    if (s_fqk_local[i] == 0) {
        flash_msg("That quick key has no favourites assigned");
        return;
    }
    s_fqk_local[i] = s_fqk_local[i] == 2 ? 1 : 2;
    fqk_paint(i);
    if (!scanner_fqk_set(s_fqk_local)) {
        flash_msg("Command queue full");
    }
}

static void fqk_refresh_cb(lv_event_t *e)
{
    (void)e;
    scanner_fqk_refresh();
    lv_label_set_text(s_fqk_status, "Refreshing...");
}

static void build_fqk(lv_obj_t *pg)
{
    int bw = 92, bh = 62, gap = 8;
    for (int i = 0; i < FQK_COUNT; i++) {
        char t[4];
        snprintf(t, sizeof(t), "%02d", i);
        s_fqk_btn[i] = mk_button(pg, t, bw, bh, 0x20262E, fqk_cb, (void *)(intptr_t)i);
        lv_obj_set_pos(s_fqk_btn[i], (i % 10) * (bw + gap), (i / 10) * (bh + gap));
    }
    int x = 10 * (bw + gap) + 10;
    lv_obj_t *rb = mk_button(pg, LV_SYMBOL_REFRESH " Refresh", CONTENT_W - 16 - x, 90, COL_ACCENT, fqk_refresh_cb, NULL);
    lv_obj_set_pos(rb, x, 0);
    s_fqk_status = mk_label(pg, &lv_font_montserrat_20, COL_MUTED, "Not loaded");
    lv_obj_set_pos(s_fqk_status, x, 110);
    lv_obj_set_width(s_fqk_status, CONTENT_W - 16 - x);
    lv_label_set_long_mode(s_fqk_status, LV_LABEL_LONG_WRAP);
    lv_obj_t *leg = mk_label(pg, &lv_font_montserrat_20, COL_MUTED,
                             "Green: on\nRed: off\nGrey: unused\n\nTap to toggle.");
    lv_obj_set_pos(leg, x, 200);
}

static void update_fqk(const scanner_state_t *s)
{
    if (!s->fqk_valid) {
        return;
    }
    memcpy(s_fqk_local, s->fqk, FQK_COUNT);
    int on = 0, off = 0;
    for (int i = 0; i < FQK_COUNT; i++) {
        fqk_paint(i);
        on += s->fqk[i] == 2;
        off += s->fqk[i] == 1;
    }
    lv_label_set_text_fmt(s_fqk_status, "%d on, %d off", on, off);
}

/* -------------------------------------------------------- waterfall page -- */

static void wf_build_lut(void)
{
    /* black -> blue -> cyan -> yellow -> red -> white */
    static const uint8_t stops[][3] = {
        {0, 0, 0}, {0, 0, 160}, {0, 180, 220}, {240, 230, 0}, {230, 40, 0}, {255, 255, 255},
    };
    const int ns = sizeof(stops) / sizeof(stops[0]);
    for (int i = 0; i < 256; i++) {
        float f = i / 255.0f * (ns - 1);
        int k = (int)f;
        if (k >= ns - 1) {
            k = ns - 2;
        }
        float t = f - k;
        uint8_t r = (uint8_t)(stops[k][0] + t * (stops[k + 1][0] - stops[k][0]));
        uint8_t g = (uint8_t)(stops[k][1] + t * (stops[k + 1][1] - stops[k][1]));
        uint8_t b = (uint8_t)(stops[k][2] + t * (stops[k + 1][2] - stops[k][2]));
        s_wf_lut[i] = lv_color_to_u16(lv_color_make(r, g, b));
    }
}

static void wf_btn_cb(lv_event_t *e)
{
    (void)e;
    bool on = !s_sc->wf_active;
    scanner_waterfall(on);
    lv_label_set_text(s_wf_status, on ? "Starting waterfall mode..." : "Returning to scan mode...");
}

static void build_waterfall(lv_obj_t *pg)
{
    wf_build_lut();
    lv_obj_t *b = mk_button(pg, "", 260, 90, COL_ACCENT, wf_btn_cb, NULL);
    lv_obj_set_pos(b, 0, 0);
    s_wf_btn_label = lv_obj_get_child(b, 0);
    lv_label_set_text(s_wf_btn_label, LV_SYMBOL_PLAY " Start");
    s_wf_status = mk_label(pg, &lv_font_montserrat_20, COL_MUTED,
                           "Start switches the scanner to waterfall mode; Stop returns it to scan mode.");
    lv_obj_set_pos(s_wf_status, 280, 30);
    lv_obj_set_width(s_wf_status, CONTENT_W - 300);
    lv_label_set_long_mode(s_wf_status, LV_LABEL_LONG_DOT);

    s_wf_chart = lv_chart_create(pg);
    lv_obj_set_pos(s_wf_chart, 0, 100);
    lv_obj_set_size(s_wf_chart, WF_W, 170);
    lv_chart_set_type(s_wf_chart, LV_CHART_TYPE_LINE);
    lv_chart_set_point_count(s_wf_chart, WF_BINS);
    lv_chart_set_range(s_wf_chart, LV_CHART_AXIS_PRIMARY_Y, 0, 255);
    lv_chart_set_div_line_count(s_wf_chart, 4, 8);
    lv_obj_set_style_size(s_wf_chart, 0, 0, LV_PART_INDICATOR);
    lv_obj_set_style_bg_color(s_wf_chart, lv_color_hex(0x000000), 0);
    lv_obj_set_style_border_width(s_wf_chart, 0, 0);
    lv_obj_set_style_pad_all(s_wf_chart, 0, 0);
    s_wf_series = lv_chart_add_series(s_wf_chart, lv_color_hex(COL_AMBER), LV_CHART_AXIS_PRIMARY_Y);
    lv_chart_set_all_value(s_wf_chart, s_wf_series, 0);

    s_wf_buf = heap_caps_calloc(WF_W * WF_H, sizeof(uint16_t), MALLOC_CAP_SPIRAM);
    if (!s_wf_buf) {
        ESP_LOGE(TAG, "no memory for waterfall canvas");
        return;
    }
    s_wf_canvas = lv_canvas_create(pg);
    lv_canvas_set_buffer(s_wf_canvas, s_wf_buf, WF_W, WF_H, LV_COLOR_FORMAT_RGB565);
    lv_obj_set_pos(s_wf_canvas, 0, 280);
    lv_obj_set_height(s_wf_canvas, CONTENT_H - 16 - 280 < WF_H ? CONTENT_H - 16 - 280 : WF_H);
}

static void update_waterfall(const scanner_state_t *s)
{
    if (!s_wf_buf) {
        return;
    }
    int lo = 255, hi = 0;
    int32_t *ys = lv_chart_get_y_array(s_wf_chart, s_wf_series);
    for (int i = 0; i < WF_BINS; i++) {
        int v = s->wf_bins[i];
        lo = v < lo ? v : lo;
        hi = v > hi ? v : hi;
        ys[i] = v;
    }
    lv_chart_refresh(s_wf_chart);
    /* slow automatic contrast */
    s_wf_lo += (lo - s_wf_lo) * 0.1f;
    s_wf_hi += (hi - s_wf_hi) * 0.1f;
    float span = s_wf_hi - s_wf_lo;
    if (span < 8) {
        span = 8;
    }
    memmove(s_wf_buf + WF_W, s_wf_buf, (size_t)WF_W * (WF_H - 1) * sizeof(uint16_t));
    for (int i = 0; i < WF_BINS; i++) {
        float n = (s->wf_bins[i] - s_wf_lo) / span;
        int idx = n <= 0 ? 0 : (n >= 1 ? 255 : (int)(n * 255));
        uint16_t c = s_wf_lut[idx];
        for (int k = 0; k < WF_PX; k++) {
            s_wf_buf[i * WF_PX + k] = c;
        }
    }
    lv_obj_invalidate(s_wf_canvas);
}

/* ------------------------------------------------------------ audio page -- */

static void au_btn_cb(lv_event_t *e)
{
    (void)e;
    audio_status_t st;
    audio_get_status(&st);
    if (st.state == AUDIO_IDLE || st.state == AUDIO_ERROR) {
        audio_start();
    } else {
        audio_stop();
    }
}

static void au_vol_cb(lv_event_t *e)
{
    int v = lv_slider_get_value(s_au_vol);
    lv_label_set_text_fmt(s_au_vol_label, "Panel speaker volume  %d%%", v);
    audio_set_local_volume((uint8_t)v);
    if (lv_event_get_code(e) == LV_EVENT_RELEASED) {
        settings_set_local_volume((uint8_t)v);
    }
}

static void build_audio(lv_obj_t *pg)
{
    s_au_btn = mk_button(pg, "", 420, 160, COL_GOOD, au_btn_cb, NULL);
    lv_obj_set_pos(s_au_btn, 0, 0);
    s_au_btn_label = lv_obj_get_child(s_au_btn, 0);
    lv_obj_set_style_text_font(s_au_btn_label, &lv_font_montserrat_40, 0);
    lv_label_set_text(s_au_btn_label, LV_SYMBOL_PLAY " Listen");

    s_au_state = mk_label(pg, &lv_font_montserrat_32, COL_TEXT, "Stopped");
    lv_obj_set_pos(s_au_state, 460, 20);
    s_au_stats = mk_label(pg, &lv_font_montserrat_24, COL_MUTED, "");
    lv_obj_set_pos(s_au_stats, 460, 80);

    int v = settings_get().local_volume;
    s_au_vol_label = mk_label(pg, &lv_font_montserrat_24, COL_TEXT, "");
    lv_label_set_text_fmt(s_au_vol_label, "Panel speaker volume  %d%%", v);
    lv_obj_set_pos(s_au_vol_label, 0, 220);
    s_au_vol = lv_slider_create(pg);
    lv_slider_set_range(s_au_vol, 0, 100);
    lv_slider_set_value(s_au_vol, v, LV_ANIM_OFF);
    lv_obj_set_size(s_au_vol, 800, 40);
    lv_obj_set_pos(s_au_vol, 20, 280);
    lv_obj_set_ext_click_area(s_au_vol, 24);
    lv_obj_add_event_cb(s_au_vol, au_vol_cb, LV_EVENT_VALUE_CHANGED, NULL);
    lv_obj_add_event_cb(s_au_vol, au_vol_cb, LV_EVENT_RELEASED, NULL);

    lv_obj_t *n = mk_label(pg, &lv_font_montserrat_20, COL_MUTED,
                           "Live audio uses RTSP on TCP 554 (PCMU 8 kHz) with a 200 ms jitter buffer.\n"
                           "The scanner allows one RTSP session: the panel always sends TEARDOWN when\n"
                           "you press Stop, when Wi-Fi drops and before rebooting.");
    lv_obj_set_pos(n, 0, 360);
}

static void update_audio(void)
{
    audio_status_t st;
    audio_get_status(&st);
    bool running = st.state != AUDIO_IDLE && st.state != AUDIO_ERROR;
    lv_label_set_text(s_au_btn_label, running ? LV_SYMBOL_STOP " Stop" : LV_SYMBOL_PLAY " Listen");
    lv_obj_set_style_bg_color(s_au_btn, lv_color_hex(running ? COL_BAD : COL_GOOD), 0);
    if (st.state == AUDIO_ERROR) {
        lv_label_set_text_fmt(s_au_state, "Error: %s", st.error);
    } else {
        lv_label_set_text(s_au_state, audio_state_name(st.state));
    }
    lv_label_set_text_fmt(s_au_stats, "Packets %lu   Bytes %lu\nLost %lu   Underruns %lu   Buffer %d ms",
                          (unsigned long)st.packets, (unsigned long)st.bytes, (unsigned long)st.lost,
                          (unsigned long)st.underruns, st.buffered_ms);
    lv_label_set_text_fmt(s_bar_audio, LV_SYMBOL_AUDIO " %s", audio_state_name(st.state));
    lv_obj_set_style_text_color(s_bar_audio, lv_color_hex(st.state == AUDIO_PLAYING ? COL_GOOD :
                                                          (st.state == AUDIO_ERROR ? COL_BAD : COL_MUTED)), 0);
}

/* --------------------------------------------------------- Wi-Fi overlay -- */

static void wifi_list_cb(lv_event_t *e)
{
    lv_obj_t *btn = lv_event_get_target_obj(e);
    const char *txt = lv_list_get_button_text(s_wifi_list, btn);
    if (txt) {
        /* strip the signal suffix "  (-54 dBm)" */
        char ssid[33];
        strlcpy(ssid, txt, sizeof(ssid));
        char *p = strstr(ssid, "  (");
        if (p) {
            *p = 0;
        }
        lv_textarea_set_text(s_wifi_ssid, ssid);
        lv_keyboard_set_textarea(s_wifi_kb, s_wifi_pass);
        lv_obj_add_state(s_wifi_pass, LV_STATE_FOCUSED);
        lv_obj_remove_state(s_wifi_ssid, LV_STATE_FOCUSED);
    }
}

static void wifi_scan_cb(lv_event_t *e)
{
    (void)e;
    net_scan_start();
    lv_label_set_text(s_wifi_status, "Scanning...");
}

static void wifi_connect_cb(lv_event_t *e)
{
    (void)e;
    const char *ssid = lv_textarea_get_text(s_wifi_ssid);
    const char *pass = lv_textarea_get_text(s_wifi_pass);
    if (!ssid[0]) {
        lv_label_set_text(s_wifi_status, "Enter or pick an SSID first");
        return;
    }
    net_connect(ssid, pass);
    lv_label_set_text_fmt(s_wifi_status, "Connecting to %s...", ssid);
}

static void wifi_close_cb(lv_event_t *e)
{
    (void)e;
    lv_obj_add_flag(s_wifi_ov, LV_OBJ_FLAG_HIDDEN);
}

static void wifi_ta_cb(lv_event_t *e)
{
    lv_keyboard_set_textarea(s_wifi_kb, lv_event_get_target_obj(e));
}

static void wifi_kb_cb(lv_event_t *e)
{
    if (lv_event_get_code(e) == LV_EVENT_READY) {
        wifi_connect_cb(e);
    }
}

static void wifi_show_pw_cb(lv_event_t *e)
{
    lv_obj_t *sw = lv_event_get_target_obj(e);
    lv_textarea_set_password_mode(s_wifi_pass, !lv_obj_has_state(sw, LV_STATE_CHECKED));
}

static void build_wifi_overlay(void)
{
    s_wifi_ov = lv_obj_create(lv_screen_active());
    lv_obj_set_size(s_wifi_ov, UI_H_RES, UI_V_RES);
    lv_obj_set_pos(s_wifi_ov, 0, 0);
    lv_obj_set_style_bg_color(s_wifi_ov, lv_color_hex(COL_BG), 0);
    lv_obj_set_style_border_width(s_wifi_ov, 0, 0);
    lv_obj_set_style_radius(s_wifi_ov, 0, 0);
    lv_obj_set_style_pad_all(s_wifi_ov, 16, 0);
    lv_obj_remove_flag(s_wifi_ov, LV_OBJ_FLAG_SCROLLABLE);

    lv_obj_t *t = mk_label(s_wifi_ov, &lv_font_montserrat_32, COL_TEXT, LV_SYMBOL_WIFI " Wi-Fi setup");
    lv_obj_set_pos(t, 0, 0);

    s_wifi_list = lv_list_create(s_wifi_ov);
    lv_obj_set_pos(s_wifi_list, 0, 50);
    lv_obj_set_size(s_wifi_list, 560, 400);
    lv_obj_set_style_bg_color(s_wifi_list, lv_color_hex(COL_PANEL), 0);
    lv_obj_set_style_border_width(s_wifi_list, 0, 0);

    lv_obj_t *l1 = mk_label(s_wifi_ov, &lv_font_montserrat_20, COL_MUTED, "Network name (SSID)");
    lv_obj_set_pos(l1, 590, 50);
    s_wifi_ssid = lv_textarea_create(s_wifi_ov);
    lv_textarea_set_one_line(s_wifi_ssid, true);
    lv_textarea_set_max_length(s_wifi_ssid, 32);
    lv_obj_set_pos(s_wifi_ssid, 590, 80);
    lv_obj_set_width(s_wifi_ssid, 640);
    lv_obj_set_style_text_font(s_wifi_ssid, &lv_font_montserrat_28, 0);
    lv_obj_add_event_cb(s_wifi_ssid, wifi_ta_cb, LV_EVENT_FOCUSED, NULL);

    lv_obj_t *l2 = mk_label(s_wifi_ov, &lv_font_montserrat_20, COL_MUTED, "Password");
    lv_obj_set_pos(l2, 590, 150);
    s_wifi_pass = lv_textarea_create(s_wifi_ov);
    lv_textarea_set_one_line(s_wifi_pass, true);
    lv_textarea_set_password_mode(s_wifi_pass, true);
    lv_textarea_set_max_length(s_wifi_pass, 64);
    lv_obj_set_pos(s_wifi_pass, 590, 180);
    lv_obj_set_width(s_wifi_pass, 480);
    lv_obj_set_style_text_font(s_wifi_pass, &lv_font_montserrat_28, 0);
    lv_obj_add_event_cb(s_wifi_pass, wifi_ta_cb, LV_EVENT_FOCUSED, NULL);
    lv_obj_t *sw = lv_switch_create(s_wifi_ov);
    lv_obj_set_pos(sw, 1090, 190);
    lv_obj_set_size(sw, 80, 40);
    lv_obj_add_event_cb(sw, wifi_show_pw_cb, LV_EVENT_VALUE_CHANGED, NULL);
    lv_obj_t *l3 = mk_label(s_wifi_ov, &lv_font_montserrat_16, COL_MUTED, "Show");
    lv_obj_set_pos(l3, 1180, 200);

    lv_obj_t *b;
    b = mk_button(s_wifi_ov, LV_SYMBOL_REFRESH " Scan", 200, 80, COL_PANEL2, wifi_scan_cb, NULL);
    lv_obj_set_pos(b, 590, 260);
    b = mk_button(s_wifi_ov, LV_SYMBOL_OK " Connect", 220, 80, COL_GOOD, wifi_connect_cb, NULL);
    lv_obj_set_pos(b, 800, 260);
    b = mk_button(s_wifi_ov, LV_SYMBOL_CLOSE " Close", 190, 80, COL_PANEL2, wifi_close_cb, NULL);
    lv_obj_set_pos(b, 1030, 260);

    s_wifi_status = mk_label(s_wifi_ov, &lv_font_montserrat_20, COL_TEXT, "");
    lv_obj_set_pos(s_wifi_status, 590, 360);
    lv_obj_set_width(s_wifi_status, 640);
    lv_label_set_long_mode(s_wifi_status, LV_LABEL_LONG_WRAP);

    s_wifi_kb = lv_keyboard_create(s_wifi_ov);
    lv_obj_set_size(s_wifi_kb, UI_H_RES - 32, 300);
    lv_obj_align(s_wifi_kb, LV_ALIGN_BOTTOM_MID, 0, 0);
    lv_obj_set_style_text_font(s_wifi_kb, &lv_font_montserrat_24, 0);
    lv_keyboard_set_textarea(s_wifi_kb, s_wifi_ssid);
    lv_obj_add_event_cb(s_wifi_kb, wifi_kb_cb, LV_EVENT_READY, NULL);

    lv_obj_add_flag(s_wifi_ov, LV_OBJ_FLAG_HIDDEN);
}

static void open_wifi_overlay(void)
{
    app_settings_t cfg = settings_get();
    lv_textarea_set_text(s_wifi_ssid, cfg.wifi_ssid);
    lv_textarea_set_text(s_wifi_pass, "");
    lv_obj_remove_flag(s_wifi_ov, LV_OBJ_FLAG_HIDDEN);
    lv_obj_move_foreground(s_wifi_ov);
    net_scan_start();
    lv_label_set_text(s_wifi_status, "Scanning...");
}

static void update_wifi_overlay(const net_status_t *ns)
{
    if (lv_obj_has_flag(s_wifi_ov, LV_OBJ_FLAG_HIDDEN)) {
        return;
    }
    static net_ap_t aps[NET_MAX_SCAN];
    uint32_t seq;
    int n = net_scan_results(aps, NET_MAX_SCAN, &seq);
    if (seq != s_wifi_scan_seen) {
        s_wifi_scan_seen = seq;
        lv_obj_clean(s_wifi_list);
        for (int i = 0; i < n; i++) {
            char t[64];
            snprintf(t, sizeof(t), "%.32s  (%d dBm)", aps[i].ssid, aps[i].rssi);
            lv_obj_t *b = lv_list_add_button(s_wifi_list, aps[i].secure ? LV_SYMBOL_EYE_CLOSE : LV_SYMBOL_WIFI, t);
            lv_obj_set_height(b, 64);
            lv_obj_set_style_text_font(b, &lv_font_montserrat_24, 0);
            lv_obj_add_event_cb(b, wifi_list_cb, LV_EVENT_CLICKED, NULL);
        }
        if (!n) {
            lv_list_add_text(s_wifi_list, "No networks found - tap Scan");
        }
    }
    if (ns->connected) {
        lv_label_set_text_fmt(s_wifi_status, "Connected to %s, IP %s. You can close this screen.", ns->ssid, ns->ip);
    } else if (!net_scan_busy() && ns->connecting) {
        lv_label_set_text_fmt(s_wifi_status, "Connecting to %s... (last reason %d)", ns->ssid, ns->last_reason);
    } else if (!net_scan_busy() && n) {
        lv_label_set_text(s_wifi_status, "Pick a network, enter the password and tap Connect.");
    }
}

/* ---------------------------------------------------------- settings page -- */

static void kb_event_cb(lv_event_t *e)
{
    lv_event_code_t code = lv_event_get_code(e);
    if (code == LV_EVENT_READY || code == LV_EVENT_CANCEL) {
        lv_obj_add_flag(s_kb, LV_OBJ_FLAG_HIDDEN);
        lv_obj_remove_state(s_set_ip_ta, LV_STATE_FOCUSED);
    }
}

static void ip_ta_cb(lv_event_t *e)
{
    (void)e;
    lv_keyboard_set_textarea(s_kb, s_set_ip_ta);
    lv_obj_remove_flag(s_kb, LV_OBJ_FLAG_HIDDEN);
    lv_obj_move_foreground(s_kb);
}

static void ip_save_cb(lv_event_t *e)
{
    (void)e;
    const char *ip = lv_textarea_get_text(s_set_ip_ta);
    struct in_addr a;
    if (!inet_aton(ip, &a)) {
        lv_label_set_text(s_set_ip_status, "Not a valid IPv4 address");
        return;
    }
    audio_stop();   /* a running session belongs to the old address */
    scanner_set_ip(ip);
    lv_label_set_text_fmt(s_set_ip_status, "Saved %s", ip);
    lv_obj_add_flag(s_kb, LV_OBJ_FLAG_HIDDEN);
}

static void ip_discover_cb(lv_event_t *e)
{
    (void)e;
    net_status_t ns;
    net_status(&ns);
    if (!ns.connected) {
        lv_label_set_text(s_set_ip_status, "Connect to Wi-Fi first");
        return;
    }
    scanner_discover();
    lv_label_set_text(s_set_ip_status, "Searching the local /24 for an SDS scanner...");
}

static void wifi_open_cb(lv_event_t *e)
{
    (void)e;
    open_wifi_overlay();
}

static void bl_cb(lv_event_t *e)
{
    int v = lv_slider_get_value(s_set_bl);
    board_backlight_set((uint8_t)v);
    if (lv_event_get_code(e) == LV_EVENT_RELEASED) {
        settings_set_backlight((uint8_t)v);
    }
}

static void flip_cb(lv_event_t *e)
{
    (void)e;
    bool f = lv_obj_has_state(s_set_flip, LV_STATE_CHECKED);
    settings_set_flip(f);
    board_display_set_flip(f);
}

static void rev_apply_cb(lv_event_t *e)
{
    (void)e;
    uint8_t rev = lv_dropdown_get_selected(s_set_rev) == 0 ? 1 : 2;
    settings_set_panel_rev(rev);
    flash_msg("Panel revision saved - rebooting");
    app_reboot();
}

static void reboot_cb(lv_event_t *e)
{
    (void)e;
    flash_msg("Rebooting...");
    app_reboot();
}

static void build_settings(lv_obj_t *pg)
{
    app_settings_t cfg = settings_get();
    int colw = (CONTENT_W - 16 - 16) / 2;

    lv_obj_t *p1 = mk_panel(pg, 0, 0, colw, 250);
    mk_label(p1, &lv_font_montserrat_24, COL_TEXT, LV_SYMBOL_WIFI " Wi-Fi");
    s_set_wifi = mk_label(p1, &lv_font_montserrat_20, COL_MUTED, "");
    lv_obj_set_pos(s_set_wifi, 0, 40);
    lv_obj_set_width(s_set_wifi, colw - 30);
    lv_label_set_long_mode(s_set_wifi, LV_LABEL_LONG_WRAP);
    lv_obj_t *b = mk_button(p1, "Wi-Fi setup", 260, 80, COL_ACCENT, wifi_open_cb, NULL);
    lv_obj_set_pos(b, 0, 130);

    lv_obj_t *p2 = mk_panel(pg, colw + 16, 0, colw, 250);
    mk_label(p2, &lv_font_montserrat_24, COL_TEXT, "Scanner IP address");
    s_set_ip_ta = lv_textarea_create(p2);
    lv_textarea_set_one_line(s_set_ip_ta, true);
    lv_textarea_set_accepted_chars(s_set_ip_ta, "0123456789.");
    lv_textarea_set_max_length(s_set_ip_ta, 15);
    lv_textarea_set_placeholder_text(s_set_ip_ta, "192.168.1.211");
    lv_textarea_set_text(s_set_ip_ta, cfg.scanner_ip);
    lv_obj_set_style_text_font(s_set_ip_ta, &lv_font_montserrat_28, 0);
    lv_obj_set_pos(s_set_ip_ta, 0, 40);
    lv_obj_set_width(s_set_ip_ta, colw - 30);
    lv_obj_add_event_cb(s_set_ip_ta, ip_ta_cb, LV_EVENT_FOCUSED, NULL);
    lv_obj_add_event_cb(s_set_ip_ta, ip_ta_cb, LV_EVENT_CLICKED, NULL);
    b = mk_button(p2, LV_SYMBOL_SAVE " Save", 200, 76, COL_GOOD, ip_save_cb, NULL);
    lv_obj_set_pos(b, 0, 110);
    b = mk_button(p2, LV_SYMBOL_REFRESH " Discover", 240, 76, COL_ACCENT, ip_discover_cb, NULL);
    lv_obj_set_pos(b, 216, 110);
    s_set_ip_status = mk_label(p2, &lv_font_montserrat_16, COL_MUTED, "");
    lv_obj_set_pos(s_set_ip_status, 0, 196);
    lv_obj_set_width(s_set_ip_status, colw - 30);
    lv_label_set_long_mode(s_set_ip_status, LV_LABEL_LONG_DOT);

    lv_obj_t *p3 = mk_panel(pg, 0, 266, colw, 250);
    mk_label(p3, &lv_font_montserrat_24, COL_TEXT, "Display");
    lv_obj_t *l = mk_label(p3, &lv_font_montserrat_20, COL_MUTED, "Backlight");
    lv_obj_set_pos(l, 0, 44);
    s_set_bl = lv_slider_create(p3);
    lv_slider_set_range(s_set_bl, 5, 100);
    lv_slider_set_value(s_set_bl, cfg.backlight, LV_ANIM_OFF);
    lv_obj_set_size(s_set_bl, colw - 80, 36);
    lv_obj_set_pos(s_set_bl, 20, 84);
    lv_obj_set_ext_click_area(s_set_bl, 24);
    lv_obj_add_event_cb(s_set_bl, bl_cb, LV_EVENT_VALUE_CHANGED, NULL);
    lv_obj_add_event_cb(s_set_bl, bl_cb, LV_EVENT_RELEASED, NULL);
    l = mk_label(p3, &lv_font_montserrat_20, COL_MUTED, "Flip 180\xC2\xB0");
    lv_obj_set_pos(l, 0, 160);
    s_set_flip = lv_switch_create(p3);
    lv_obj_set_size(s_set_flip, 90, 46);
    lv_obj_set_pos(s_set_flip, 160, 150);
    if (cfg.flip) {
        lv_obj_add_state(s_set_flip, LV_STATE_CHECKED);
    }
    lv_obj_add_event_cb(s_set_flip, flip_cb, LV_EVENT_VALUE_CHANGED, NULL);

    lv_obj_t *p4 = mk_panel(pg, colw + 16, 266, colw, 250);
    mk_label(p4, &lv_font_montserrat_24, COL_TEXT, "Panel revision");
    s_set_rev = lv_dropdown_create(p4);
    lv_dropdown_set_options(s_set_rev, "Original (batch <= 2627)\nV2 (batch >= 2628)");
    lv_dropdown_set_selected(s_set_rev, cfg.panel_rev == 1 ? 0 : 1);
    lv_obj_set_pos(s_set_rev, 0, 44);
    lv_obj_set_width(s_set_rev, colw - 30);
    lv_obj_set_style_text_font(s_set_rev, &lv_font_montserrat_24, 0);
    b = mk_button(p4, "Apply & reboot", 280, 76, COL_WARN, rev_apply_cb, NULL);
    lv_obj_set_pos(b, 0, 130);

    lv_obj_t *p5 = mk_panel(pg, 0, 532, CONTENT_W - 16, CONTENT_H - 16 - 532);
    const esp_app_desc_t *d = esp_app_get_description();
    lv_obj_t *v = mk_label(p5, &lv_font_montserrat_20, COL_MUTED, "");
    lv_label_set_text_fmt(v, "Firmware %s (%s %s)  |  ESP-IDF %s  |  Panel V%u  |  Free PSRAM %u KB",
                          d->version, d->date, d->time, esp_get_idf_version(), cfg.panel_rev,
                          (unsigned)(heap_caps_get_free_size(MALLOC_CAP_SPIRAM) / 1024));
    lv_obj_set_pos(v, 0, 0);
    b = mk_button(p5, LV_SYMBOL_POWER " Reboot", 220, 76, COL_BAD, reboot_cb, NULL);
    lv_obj_align(b, LV_ALIGN_BOTTOM_RIGHT, 0, 0);

    s_kb = lv_keyboard_create(lv_screen_active());
    lv_keyboard_set_mode(s_kb, LV_KEYBOARD_MODE_NUMBER);
    lv_obj_set_size(s_kb, 640, 320);
    lv_obj_align(s_kb, LV_ALIGN_BOTTOM_RIGHT, 0, 0);
    lv_obj_set_style_text_font(s_kb, &lv_font_montserrat_28, 0);
    lv_obj_add_event_cb(s_kb, kb_event_cb, LV_EVENT_READY, NULL);
    lv_obj_add_event_cb(s_kb, kb_event_cb, LV_EVENT_CANCEL, NULL);
    lv_obj_add_flag(s_kb, LV_OBJ_FLAG_HIDDEN);
}

/* ------------------------------------------------------------ refresh -- */

static void refresh_cb(lv_timer_t *t)
{
    (void)t;
    scanner_get_state(s_sc);
    const scanner_state_t *s = s_sc;
    net_status_t ns;
    net_status(&ns);

    /* status bar */
    if (!s->ip[0]) {
        lv_label_set_text(s_bar_scanner, LV_SYMBOL_WARNING " No scanner IP");
        lv_obj_set_style_text_color(s_bar_scanner, lv_color_hex(COL_WARN), 0);
    } else {
        lv_label_set_text_fmt(s_bar_scanner, "%s %s %s  %s", s->online ? LV_SYMBOL_OK : LV_SYMBOL_CLOSE,
                              s->model[0] ? s->model : "Scanner", s->ip, s->online ? "online" : "offline");
        lv_obj_set_style_text_color(s_bar_scanner, lv_color_hex(s->online ? COL_GOOD : COL_BAD), 0);
    }
    if (ns.connected) {
        lv_label_set_text_fmt(s_bar_wifi, LV_SYMBOL_WIFI " %s  %s  %d dBm", ns.ssid, ns.ip, ns.rssi);
        lv_obj_set_style_text_color(s_bar_wifi, lv_color_hex(COL_TEXT), 0);
    } else {
        lv_label_set_text_fmt(s_bar_wifi, LV_SYMBOL_WIFI " %s", ns.ssid[0] ? "connecting..." : "not configured");
        lv_obj_set_style_text_color(s_bar_wifi, lv_color_hex(COL_WARN), 0);
    }
    if (s->last_error[0]) {
        lv_label_set_text(s_bar_msg, s->last_error);
    }

    if (s->gsi_seq != s_seen_gsi) {
        s_seen_gsi = s->gsi_seq;
        update_live(s);
    }
    if (s->sts_seq != s_seen_sts) {
        s_seen_sts = s->sts_seq;
        set_hidden(s_mirror_note, s->sts_count > 0);
        lv_obj_invalidate(s_mirror);
    }
    if (s->fqk_seq != s_seen_fqk) {
        s_seen_fqk = s->fqk_seq;
        update_fqk(s);
    }
    if (s->wf_seq != s_seen_wf) {
        s_seen_wf = s->wf_seq;
        update_waterfall(s);
    }
    lv_label_set_text(s_wf_btn_label, s->wf_active ? LV_SYMBOL_STOP " Stop" : LV_SYMBOL_PLAY " Start");
    if (s->wf_active) {
        lv_label_set_text_fmt(s_wf_status, "Waterfall running - %lu frames", (unsigned long)s->wf_seq);
    }

    if (s->discover_seq != s_seen_disc) {
        s_seen_disc = s->discover_seq;
        if (s->discovered_ip[0]) {
            lv_textarea_set_text(s_set_ip_ta, s->discovered_ip);
            lv_label_set_text_fmt(s_set_ip_status, "Found scanner at %s (saved)", s->discovered_ip);
        } else {
            lv_label_set_text(s_set_ip_status, "No scanner answered MDL on the local /24");
        }
    }

    lv_label_set_text_fmt(s_set_wifi, "%s\nSSID: %s\nIP: %s   RSSI: %d dBm   Drops: %lu",
                          ns.connected ? "Connected" : (ns.connecting ? "Connecting..." : "Not connected"),
                          ns.ssid[0] ? ns.ssid : "-", ns.connected ? ns.ip : "-", ns.connected ? ns.rssi : 0,
                          (unsigned long)ns.disconnects);

    update_audio();
    update_wifi_overlay(&ns);

    /* first-run guidance: once Wi-Fi is up and no scanner IP is known, try discovery once */
    static bool auto_disc_done;
    if (ns.connected && !s->ip[0] && !auto_disc_done) {
        auto_disc_done = true;
        show_page(PG_SETTINGS);
        scanner_discover();
        lv_label_set_text(s_set_ip_status, "Searching the local /24 for an SDS scanner...");
    }
}

/* --------------------------------------------------------------- build -- */

void ui_init(lv_display_t *disp)
{
    (void)disp;
    s_sc = heap_caps_calloc(1, sizeof(scanner_state_t), MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    assert(s_sc);

    lv_obj_t *scr = lv_screen_active();
    lv_obj_set_style_bg_color(scr, lv_color_hex(COL_BG), 0);
    lv_obj_set_style_text_color(scr, lv_color_hex(COL_TEXT), 0);
    lv_obj_set_style_text_font(scr, &lv_font_montserrat_20, 0);
    lv_obj_remove_flag(scr, LV_OBJ_FLAG_SCROLLABLE);

    /* status bar */
    lv_obj_t *bar = lv_obj_create(scr);
    lv_obj_set_pos(bar, 0, 0);
    lv_obj_set_size(bar, UI_H_RES, BAR_H);
    lv_obj_set_style_bg_color(bar, lv_color_hex(COL_PANEL), 0);
    lv_obj_set_style_border_width(bar, 0, 0);
    lv_obj_set_style_radius(bar, 0, 0);
    lv_obj_set_style_pad_hor(bar, 16, 0);
    lv_obj_set_style_pad_ver(bar, 0, 0);
    lv_obj_remove_flag(bar, LV_OBJ_FLAG_SCROLLABLE);
    lv_obj_t *title = mk_label(bar, &lv_font_montserrat_24, COL_TEXT, APP_NAME);
    lv_obj_align(title, LV_ALIGN_LEFT_MID, 0, 0);
    s_bar_scanner = mk_label(bar, &lv_font_montserrat_20, COL_MUTED, "");
    lv_obj_align(s_bar_scanner, LV_ALIGN_LEFT_MID, 200, 0);
    s_bar_msg = mk_label(bar, &lv_font_montserrat_16, COL_WARN, "");
    lv_obj_align(s_bar_msg, LV_ALIGN_LEFT_MID, 560, 0);
    lv_obj_set_width(s_bar_msg, 200);
    lv_label_set_long_mode(s_bar_msg, LV_LABEL_LONG_DOT);
    s_bar_audio = mk_label(bar, &lv_font_montserrat_20, COL_MUTED, "");
    lv_obj_align(s_bar_audio, LV_ALIGN_RIGHT_MID, -420, 0);
    s_bar_wifi = mk_label(bar, &lv_font_montserrat_20, COL_MUTED, "");
    lv_obj_align(s_bar_wifi, LV_ALIGN_RIGHT_MID, 0, 0);
    lv_obj_set_width(s_bar_wifi, 400);
    lv_obj_set_style_text_align(s_bar_wifi, LV_TEXT_ALIGN_RIGHT, 0);
    lv_label_set_long_mode(s_bar_wifi, LV_LABEL_LONG_DOT);

    /* navigation */
    int nh = (CONTENT_H - 16 - (PAGE_COUNT - 1) * 10) / PAGE_COUNT;
    for (int i = 0; i < PAGE_COUNT; i++) {
        s_nav[i] = mk_button(scr, s_page_names[i], NAV_W - 16, nh, COL_PANEL2, nav_cb, (void *)(intptr_t)i);
        lv_obj_set_pos(s_nav[i], 8, BAR_H + 8 + i * (nh + 10));
        lv_obj_set_style_text_font(lv_obj_get_child(s_nav[i], 0), &lv_font_montserrat_20, 0);
    }

    /* pages */
    for (int i = 0; i < PAGE_COUNT; i++) {
        lv_obj_t *p = lv_obj_create(scr);
        lv_obj_remove_style_all(p);
        lv_obj_set_pos(p, NAV_W + 8, BAR_H + 8);
        lv_obj_set_size(p, CONTENT_W - 8, CONTENT_H - 8);
        lv_obj_remove_flag(p, LV_OBJ_FLAG_SCROLLABLE);
        lv_obj_add_flag(p, LV_OBJ_FLAG_HIDDEN);
        s_pages[i] = p;
    }
    build_live(s_pages[PG_LIVE]);
    build_remote(s_pages[PG_REMOTE]);
    build_fqk(s_pages[PG_FQK]);
    build_waterfall(s_pages[PG_WF]);
    build_audio(s_pages[PG_AUDIO]);
    build_settings(s_pages[PG_SETTINGS]);
    build_wifi_overlay();

    show_page(PG_LIVE);
    lv_timer_create(refresh_cb, 100, NULL);

    if (!settings_get().wifi_ssid[0]) {
        open_wifi_overlay();
    }
    ESP_LOGI(TAG, "UI ready");
}
