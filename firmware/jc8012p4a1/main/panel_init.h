/* JD9365 initialisation tables for both JC8012P4A1 panel revisions. */
#pragma once
#include <stddef.h>
#include <stdint.h>
#include "esp_lcd_jd9365.h"

extern const jd9365_lcd_init_cmd_t jc8012_init_v1[];
extern const size_t jc8012_init_v1_count;
extern const jd9365_lcd_init_cmd_t jc8012_init_v2[];
extern const size_t jc8012_init_v2_count;
