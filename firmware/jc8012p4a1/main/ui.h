/* LVGL user interface (landscape 1280x800). */
#pragma once
#include "lvgl.h"

/* Builds the UI. Must be called with the LVGL port lock held. */
void ui_init(lv_display_t *disp);
