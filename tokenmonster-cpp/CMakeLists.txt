cmake_minimum_required(VERSION 3.20)
project(tokenmonster_cpp VERSION 0.1.0 LANGUAGES CXX)

include(CMakePackageConfigHelpers)
include(GNUInstallDirs)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

option(TOKENMONSTER_CPP_BUILD_TESTS "Build tokenmonster-cpp tests" ${PROJECT_IS_TOP_LEVEL})
set(TOKENMONSTER_CPP_CAPCODE_SOURCE_DIR "" CACHE PATH "Optional path to a capcode-cpp source checkout")

find_package(PkgConfig REQUIRED)
pkg_check_modules(ICU REQUIRED IMPORTED_TARGET icu-uc icu-i18n)

if(NOT TARGET capcode::capcode)
  if(TOKENMONSTER_CPP_CAPCODE_SOURCE_DIR)
    add_subdirectory("${TOKENMONSTER_CPP_CAPCODE_SOURCE_DIR}" capcode-cpp-build EXCLUDE_FROM_ALL)
  elseif(EXISTS "${CMAKE_CURRENT_LIST_DIR}/../capcode-cpp/CMakeLists.txt")
    add_subdirectory("${CMAKE_CURRENT_LIST_DIR}/../capcode-cpp" capcode-cpp-build EXCLUDE_FROM_ALL)
  else()
    find_package(capcode_cpp CONFIG REQUIRED)
  endif()
endif()

add_library(tokenmonster_cpp
  src/tokenmonster.cpp
)
add_library(tokenmonster::tokenmonster ALIAS tokenmonster_cpp)
set_target_properties(tokenmonster_cpp PROPERTIES EXPORT_NAME tokenmonster)

target_include_directories(tokenmonster_cpp
  PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
    $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
)

target_link_libraries(tokenmonster_cpp
  PUBLIC
    capcode::capcode
  PRIVATE
    PkgConfig::ICU
)

target_compile_features(tokenmonster_cpp PUBLIC cxx_std_20)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
  target_compile_options(tokenmonster_cpp PRIVATE -Wall -Wextra -Wpedantic)
endif()

install(TARGETS tokenmonster_cpp
  EXPORT tokenmonster_cppTargets
  ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
  LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
  RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
  INCLUDES DESTINATION ${CMAKE_INSTALL_INCLUDEDIR}
)
install(DIRECTORY include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})

write_basic_package_version_file(
  "${CMAKE_CURRENT_BINARY_DIR}/tokenmonster_cppConfigVersion.cmake"
  VERSION ${PROJECT_VERSION}
  COMPATIBILITY SameMajorVersion
)

configure_package_config_file(
  cmake/tokenmonster_cppConfig.cmake.in
  "${CMAKE_CURRENT_BINARY_DIR}/tokenmonster_cppConfig.cmake"
  INSTALL_DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/tokenmonster_cpp
)

install(FILES
  "${CMAKE_CURRENT_BINARY_DIR}/tokenmonster_cppConfig.cmake"
  "${CMAKE_CURRENT_BINARY_DIR}/tokenmonster_cppConfigVersion.cmake"
  DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/tokenmonster_cpp
)
install(EXPORT tokenmonster_cppTargets
  NAMESPACE tokenmonster::
  DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/tokenmonster_cpp
)

if(TOKENMONSTER_CPP_BUILD_TESTS)
  enable_testing()

  add_executable(tokenmonster_smoke tests/smoke.cpp)
  target_link_libraries(tokenmonster_smoke PRIVATE tokenmonster_cpp)

  add_executable(tokenmonster_unit tests/unit.cpp)
  target_link_libraries(tokenmonster_unit PRIVATE tokenmonster_cpp)
  add_test(NAME tokenmonster_unit COMMAND tokenmonster_unit)

  add_executable(tokenmonster_dump tests/dump.cpp)
  target_link_libraries(tokenmonster_dump PRIVATE tokenmonster_cpp)

  add_executable(tokenmonster_bench tests/bench.cpp)
  target_link_libraries(tokenmonster_bench PRIVATE tokenmonster_cpp)
endif()
