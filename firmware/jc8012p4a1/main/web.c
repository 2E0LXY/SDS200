/*
 * Minimal HTTP API.
 *
 *   GET  /api/state         -> JSON snapshot of the scanner state
 *   POST /api/control/key   -> {"key":"M"} queues KEY,M,P
 *
 * No authentication: intended for a trusted home network only.
 */
#include <string.h>
#include <stdlib.h>
#include "esp_http_server.h"
#include "esp_log.h"
#include "cJSON.h"
#include "scanner.h"
#include "web.h"

static const char *TAG = "web";

static bool hold_on(const char *h)
{
    return h && (strcasecmp(h, "On") == 0 || strcasecmp(h, "Hold") == 0 || strcasecmp(h, "True") == 0);
}

static esp_err_t send_json(httpd_req_t *req, cJSON *root, const char *status)
{
    char *txt = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!txt) {
        return httpd_resp_send_500(req);
    }
    httpd_resp_set_status(req, status);
    httpd_resp_set_type(req, "application/json");
    httpd_resp_set_hdr(req, "Cache-Control", "no-store");
    esp_err_t r = httpd_resp_sendstr(req, txt);
    free(txt);
    return r;
}

static void add_int_or_null(cJSON *o, const char *k, int v, int unknown)
{
    if (v == unknown) {
        cJSON_AddNullToObject(o, k);
    } else {
        cJSON_AddNumberToObject(o, k, v);
    }
}

static esp_err_t state_get(httpd_req_t *req)
{
    scanner_state_t *s = malloc(sizeof(*s));
    if (!s) {
        return httpd_resp_send_500(req);
    }
    scanner_get_state(s);
    cJSON *o = cJSON_CreateObject();
    cJSON_AddBoolToObject(o, "online", s->online);
    cJSON_AddStringToObject(o, "mode", s->mode);
    cJSON_AddStringToObject(o, "system", s->system);
    cJSON_AddStringToObject(o, "department", s->department);
    cJSON_AddStringToObject(o, "site", s->site);
    cJSON_AddStringToObject(o, "channel", s->channel);
    cJSON_AddStringToObject(o, "frequency", s->frequency);
    cJSON_AddStringToObject(o, "tgid", s->tgid);
    cJSON_AddStringToObject(o, "unit_id", s->unit_id);
    cJSON_AddStringToObject(o, "modulation", s->modulation);
    cJSON_AddStringToObject(o, "service_type", s->service_type);
    add_int_or_null(o, "volume", s->volume, -1);
    add_int_or_null(o, "squelch", s->squelch, -1);
    add_int_or_null(o, "rssi", s->rssi, 0);
    add_int_or_null(o, "signal", s->signal, -1);
    cJSON_AddBoolToObject(o, "system_hold", hold_on(s->system_hold));
    cJSON_AddBoolToObject(o, "department_hold", hold_on(s->department_hold));
    cJSON_AddBoolToObject(o, "channel_hold", hold_on(s->channel_hold));
    free(s);
    return send_json(req, o, "200 OK");
}

static esp_err_t key_post(httpd_req_t *req)
{
    char body[128];
    int total = req->content_len;
    if (total <= 0 || total >= (int)sizeof(body)) {
        httpd_resp_send_err(req, HTTPD_400_BAD_REQUEST, "body required (max 127 bytes)");
        return ESP_OK;
    }
    int got = 0;
    while (got < total) {
        int r = httpd_req_recv(req, body + got, total - got);
        if (r <= 0) {
            if (r == HTTPD_SOCK_ERR_TIMEOUT) {
                continue;
            }
            return ESP_FAIL;
        }
        got += r;
    }
    body[got] = 0;
    cJSON *in = cJSON_Parse(body);
    const cJSON *k = in ? cJSON_GetObjectItemCaseSensitive(in, "key") : NULL;
    char code = (cJSON_IsString(k) && k->valuestring && strlen(k->valuestring) == 1) ? k->valuestring[0] : 0;
    cJSON_Delete(in);

    cJSON *o = cJSON_CreateObject();
    if (!code || !scanner_key(code)) {
        cJSON_AddBoolToObject(o, "ok", false);
        cJSON_AddStringToObject(o, "error", "invalid key or queue full");
        return send_json(req, o, "422 Unprocessable Entity");
    }
    cJSON_AddBoolToObject(o, "ok", true);
    char ks[2] = {code, 0};
    cJSON_AddStringToObject(o, "key", ks);
    return send_json(req, o, "200 OK");
}

void web_start(void)
{
    httpd_config_t cfg = HTTPD_DEFAULT_CONFIG();
    cfg.server_port = 80;
    cfg.stack_size = 6144;
    cfg.max_open_sockets = 4;
    cfg.lru_purge_enable = true;
    httpd_handle_t h = NULL;
    if (httpd_start(&h, &cfg) != ESP_OK) {
        ESP_LOGE(TAG, "HTTP server failed to start");
        return;
    }
    static const httpd_uri_t u_state = {.uri = "/api/state", .method = HTTP_GET, .handler = state_get};
    static const httpd_uri_t u_key = {.uri = "/api/control/key", .method = HTTP_POST, .handler = key_post};
    httpd_register_uri_handler(h, &u_state);
    httpd_register_uri_handler(h, &u_key);
    ESP_LOGI(TAG, "HTTP API on port 80");
}
