#pragma once

#include <chrono>
#include <mutex>
#include <string>
#include <vector>

enum class openloader_access_decision {
    allowed,
    invalid_api_key,
    ip_not_allowed,
    policy_unavailable,
};

// Loads the OpenLoader policy snapshot locally. The request path never calls the
// OpenLoader management service; it only refreshes this small file periodically.
class openloader_access_control {
public:
    explicit openloader_access_control(std::string path);

    bool configured() const;
    openloader_access_decision authorize(
        const std::string & remote_address,
        const std::string & api_key,
        bool validate_api_key);
    std::string last_error() const;

private:
    struct snapshot {
        bool valid = false;
        bool api_key_enabled = false;
        bool ip_allowlist_enabled = false;
        std::vector<std::string> api_key_hashes;
        std::vector<std::string> ip_allowlist;
    };

    void refresh_locked();

    const std::string path;
    mutable std::mutex mutex;
    snapshot current;
    std::string error;
    std::chrono::steady_clock::time_point next_refresh = std::chrono::steady_clock::time_point::min();
};
