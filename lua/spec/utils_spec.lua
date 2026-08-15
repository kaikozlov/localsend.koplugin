require("busted.runner")()

-- Tests for localsend_utils.lua - utility functions tested directly

describe("LocalSend Utils", function()
    local lsutils

    setup(function()
        lsutils = require("localsend_utils")
    end)

    describe("KOReader path integration", function()
        it("derives the module directory from the loaded Lua source", function()
            assert.equal("/custom/plugins/localsend.koplugin", lsutils.moduleDir("@/custom/plugins/localsend.koplugin/main.lua", "/fallback"))
        end)

        it("prefers a complete extra-plugin-path installation", function()
            local exists = function(path)
                return path == "/extra/localsend.koplugin/localsend" or path == "/canonical/localsend.koplugin/localsend"
            end
            assert.equal("/extra/localsend.koplugin", lsutils.resolvePluginDir("/extra/localsend.koplugin", "/canonical/localsend.koplugin", exists))
        end)

        it("falls back only when the source tree is incomplete", function()
            local exists = function(path)
                return path == "/canonical/localsend.koplugin/localsend"
            end
            assert.equal(
                "/canonical/localsend.koplugin",
                lsutils.resolvePluginDir("/source/localsend.koplugin", "/canonical/localsend.koplugin", exists)
            )
        end)

        it("uses configured KOReader home before Device.home_dir", function()
            local settings = {
                readSetting = function(_, key)
                    return key == "home_dir" and "/configured/home" or nil
                end,
            }
            assert.equal("/configured/home", lsutils.defaultSaveDir({ home_dir = "/device/home" }, settings, "/data"))
        end)

        it("uses Device.home_dir then data dir as fallbacks", function()
            local settings = {
                readSetting = function()
                    return nil
                end,
            }
            assert.equal("/device/home", lsutils.defaultSaveDir({ home_dir = "/device/home" }, settings, "/data"))
            assert.equal("/data", lsutils.defaultSaveDir({}, settings, "/data"))
        end)
    end)

    describe("isValidPath", function()
        it("rejects nil path", function()
            assert.is_false(lsutils.isValidPath(nil))
        end)

        it("rejects empty path", function()
            assert.is_false(lsutils.isValidPath(""))
        end)

        it("rejects relative paths", function()
            assert.is_false(lsutils.isValidPath("relative/path"))
            assert.is_false(lsutils.isValidPath("./relative"))
            assert.is_false(lsutils.isValidPath("../parent"))
        end)

        it("accepts absolute paths", function()
            assert.is_true(lsutils.isValidPath("/absolute/path"))
            assert.is_true(lsutils.isValidPath("/"))
            assert.is_true(lsutils.isValidPath("/mnt/us/documents"))
        end)

        it("rejects paths with null bytes", function()
            assert.is_false(lsutils.isValidPath("/path\0/with/null"))
        end)

        it("rejects paths with command substitution", function()
            assert.is_false(lsutils.isValidPath("/path/$(whoami)"))
            assert.is_false(lsutils.isValidPath("/path/`id`"))
        end)

        it("accepts paths with spaces", function()
            assert.is_true(lsutils.isValidPath("/path/with spaces/file"))
        end)

        it("accepts paths with special but safe characters", function()
            assert.is_true(lsutils.isValidPath("/path/with-dash"))
            assert.is_true(lsutils.isValidPath("/path/with_underscore"))
            assert.is_true(lsutils.isValidPath("/path/with.dot"))
        end)
    end)

    describe("isValidPort", function()
        it("rejects nil port", function()
            assert.is_false(lsutils.isValidPort(nil))
        end)

        it("rejects non-numeric strings", function()
            assert.is_false(lsutils.isValidPort("abc"))
            assert.is_false(lsutils.isValidPort(""))
        end)

        it("rejects port 0", function()
            assert.is_false(lsutils.isValidPort(0))
            assert.is_false(lsutils.isValidPort("0"))
        end)

        it("rejects negative ports", function()
            assert.is_false(lsutils.isValidPort(-1))
            assert.is_false(lsutils.isValidPort("-80"))
        end)

        it("rejects ports above 65535", function()
            assert.is_false(lsutils.isValidPort(65536))
            assert.is_false(lsutils.isValidPort("70000"))
        end)

        it("accepts valid port numbers", function()
            assert.is_true(lsutils.isValidPort(1))
            assert.is_true(lsutils.isValidPort(80))
            assert.is_true(lsutils.isValidPort(443))
            assert.is_true(lsutils.isValidPort(53317))
            assert.is_true(lsutils.isValidPort(65535))
        end)

        it("accepts port numbers as strings", function()
            assert.is_true(lsutils.isValidPort("80"))
            assert.is_true(lsutils.isValidPort("53317"))
        end)

        it("rejects floating point ports", function()
            assert.is_false(lsutils.isValidPort(80.5))
            assert.is_false(lsutils.isValidPort("80.5"))
        end)
    end)

    describe("compareVersions", function()
        it("returns 0 for equal versions", function()
            assert.equal(0, lsutils.compareVersions("1.0.0", "1.0.0"))
            assert.equal(0, lsutils.compareVersions("v1.0.0", "1.0.0"))
            assert.equal(0, lsutils.compareVersions("1.0.0", "v1.0.0"))
            assert.equal(0, lsutils.compareVersions("v1.1.1", "v1.1.1"))
        end)

        it("returns -1 when first version is older", function()
            assert.equal(-1, lsutils.compareVersions("1.0.0", "1.0.1"))
            assert.equal(-1, lsutils.compareVersions("1.0.0", "1.1.0"))
            assert.equal(-1, lsutils.compareVersions("1.0.0", "2.0.0"))
            assert.equal(-1, lsutils.compareVersions("1.1.0", "1.1.1"))
        end)

        it("returns 1 when first version is newer", function()
            assert.equal(1, lsutils.compareVersions("1.0.1", "1.0.0"))
            assert.equal(1, lsutils.compareVersions("1.1.0", "1.0.0"))
            assert.equal(1, lsutils.compareVersions("2.0.0", "1.0.0"))
            assert.equal(1, lsutils.compareVersions("1.1.1", "1.1.0"))
        end)

        it("handles versions with different segment counts", function()
            assert.equal(0, lsutils.compareVersions("1.0", "1.0.0"))
            assert.equal(-1, lsutils.compareVersions("1.0", "1.0.1"))
            assert.equal(1, lsutils.compareVersions("1.0.1", "1.0"))
        end)

        it("handles v prefix consistently", function()
            assert.equal(0, lsutils.compareVersions("v1.1.0", "v1.1.0"))
            assert.equal(-1, lsutils.compareVersions("v1.0.0", "v1.1.0"))
            assert.equal(1, lsutils.compareVersions("v1.1.1", "v1.1.0"))
        end)
    end)

    describe("findAssetForArch", function()
        local mock_assets = {
            { name = "localsend-koplugin-armv7.zip", browser_download_url = "https://example.com/armv7.zip" },
            { name = "localsend-koplugin-arm64.zip", browser_download_url = "https://example.com/arm64.zip" },
            { name = "checksums.txt", browser_download_url = "https://example.com/checksums.txt" },
        }

        it("finds armv7 asset", function()
            local url, name = lsutils.findAssetForArch(mock_assets, "armv7")
            assert.equal("https://example.com/armv7.zip", url)
            assert.equal("localsend-koplugin-armv7.zip", name)
        end)

        it("finds arm64 asset", function()
            local url, name = lsutils.findAssetForArch(mock_assets, "arm64")
            assert.equal("https://example.com/arm64.zip", url)
            assert.equal("localsend-koplugin-arm64.zip", name)
        end)

        it("returns nil for unknown arch", function()
            local url, name = lsutils.findAssetForArch(mock_assets, "x86")
            assert.is_nil(url)
            assert.is_nil(name)
        end)

        it("returns nil for empty assets", function()
            local url, name = lsutils.findAssetForArch({}, "armv7")
            assert.is_nil(url)
            assert.is_nil(name)
        end)
    end)

    describe("validateDeviceName", function()
        it("accepts empty name", function()
            local valid, err = lsutils.validateDeviceName("")
            assert.is_true(valid)
            assert.is_nil(err)
        end)

        it("accepts simple alphanumeric names", function()
            assert.is_true(lsutils.validateDeviceName("MyKindle"))
            assert.is_true(lsutils.validateDeviceName("Kindle123"))
        end)

        it("accepts names with spaces", function()
            assert.is_true(lsutils.validateDeviceName("My Kindle"))
            assert.is_true(lsutils.validateDeviceName("Special Pineapple"))
        end)

        it("accepts names with hyphens and underscores", function()
            assert.is_true(lsutils.validateDeviceName("My-Kindle"))
            assert.is_true(lsutils.validateDeviceName("My_Kindle"))
        end)

        it("accepts names with apostrophes", function()
            assert.is_true(lsutils.validateDeviceName("Kai's Kindle"))
            assert.is_true(lsutils.validateDeviceName("Kai\xe2\x80\x99s Kindle")) -- right curly
            assert.is_true(lsutils.validateDeviceName("Kai\xe2\x80\x98s Kindle")) -- left curly
        end)

        it("rejects names that are too long", function()
            local long_name = string.rep("a", 65)
            local valid, err = lsutils.validateDeviceName(long_name)
            assert.is_false(valid)
            assert.is_not_nil(err)
        end)

        it("accepts max length names", function()
            local max_name = string.rep("a", 64)
            local valid = lsutils.validateDeviceName(max_name)
            assert.is_true(valid)
        end)

        it("rejects names with shell metacharacters", function()
            local invalid_names = {
                "test;echo",
                "test`id`",
                "test$(whoami)",
                "test|cat",
                "test&bg",
                "test>file",
                "test<file",
            }
            for _, name in ipairs(invalid_names) do
                local valid = lsutils.validateDeviceName(name)
                assert.is_false(valid, "Should reject: " .. name)
            end
        end)

        it("rejects names with quotes", function()
            assert.is_false(lsutils.validateDeviceName('test"quote'))
        end)

        it("handles nil name without error", function()
            local ok, result = pcall(function()
                return lsutils.validateDeviceName(nil)
            end)
            -- Should handle nil gracefully (treat as empty = valid for random name)
            assert.is_true(ok, "validateDeviceName should not error on nil input")
            if ok then
                assert.is_true(result, "nil should be treated as valid (empty name)")
            end
        end)

        it("allows names with newlines (matched by %s pattern)", function()
            -- The implementation uses %s which matches newlines - document this behavior
            local valid = lsutils.validateDeviceName("test\nname")
            assert.is_true(valid)
        end)
    end)
end)
