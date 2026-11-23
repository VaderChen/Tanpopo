# 保留上游架構 Smoke 測試，不下載權重，也不依賴 Python。
find_package(Threads REQUIRED)
llama_add_compile_flags()
add_executable(test-llama-archs test-llama-archs.cpp)
target_compile_features(test-llama-archs PRIVATE cxx_std_17)
target_link_libraries(test-llama-archs PRIVATE llama-common)
add_test(NAME inkling2-loop-smoke COMMAND test-llama-archs -a ^inkling2_loop$ -s 1)
